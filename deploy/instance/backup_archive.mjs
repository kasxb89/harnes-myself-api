#!/usr/bin/env node
/** Private, streaming off-host archive transport. No remote deletion or extraction. */
import { createCipheriv, createDecipheriv, createHash, randomBytes, randomUUID } from 'node:crypto';
import { constants, createReadStream, createWriteStream } from 'node:fs';
import * as fs from 'node:fs/promises';
import { homedir } from 'node:os';
import path from 'node:path';
import { spawn } from 'node:child_process';
import { Transform, Writable } from 'node:stream';
import { pipeline } from 'node:stream/promises';
import { createGunzip, createZstdDecompress, constants as zlibConstants } from 'node:zlib';
import { fileURLToPath } from 'node:url';

const ALGORITHM = 'aes-256-gcm';
const MIN_FREE = 3n * 1024n ** 3n;
const DEFAULT_KEYS = path.join(homedir(), '.codex/private-keys/harnes-api-archives');
const VALIDATOR = fileURLToPath(new URL('./verify_backup_archive.py', import.meta.url));
const HEX64 = /^[a-f0-9]{64}$/;
const currentUid = () => typeof process.getuid === 'function' ? process.getuid() : undefined;
function requireValue(value, code) { if (!value) throw new Error(code); }
function canonical(value) {
  if (Array.isArray(value)) return `[${value.map(canonical).join(',')}]`;
  if (value && typeof value === 'object') return `{${Object.keys(value).sort().map(k => `${JSON.stringify(k)}:${canonical(value[k])}`).join(',')}}`;
  return JSON.stringify(value);
}
async function privateDirectory(directory) {
  await fs.mkdir(directory, { recursive: true, mode: 0o700 });
  const stat = await fs.lstat(directory);
  requireValue(stat.isDirectory() && !stat.isSymbolicLink() && (stat.mode & 0o777) === 0o700 &&
    (currentUid() === undefined || stat.uid === currentUid()), 'PRIVATE_DIRECTORY_REQUIRED');
  return fs.realpath(directory);
}
async function privateFile(filename) {
  const handle = await fs.open(filename, constants.O_RDONLY | constants.O_NOFOLLOW);
  try {
    const stat = await handle.stat({ bigint: true });
    requireValue(stat.isFile() && (stat.mode & 0o077n) === 0n &&
      (currentUid() === undefined || stat.uid === BigInt(currentUid())), 'PRIVATE_FILE_REQUIRED');
    return handle;
  } catch (error) { await handle.close(); throw error; }
}
async function readPrivate(filename) {
  const handle = await privateFile(filename);
  try { return await handle.readFile(); } finally { await handle.close(); }
}
async function exclusiveWrite(filename, value) {
  const handle = await fs.open(filename, constants.O_WRONLY | constants.O_CREAT | constants.O_EXCL | constants.O_NOFOLLOW, 0o600);
  try { await handle.writeFile(value); await handle.sync(); } finally { await handle.close(); }
}
async function syncDirectory(directory) {
  const handle = await fs.open(directory, constants.O_RDONLY);
  try { await handle.sync(); } finally { await handle.close(); }
}
async function finalizeNoOverwrite(temporary, final) {
  // link is atomic and refuses an existing destination, unlike rename overwrites.
  await fs.link(temporary, final); await fs.unlink(temporary); await syncDirectory(path.dirname(final));
}
async function capacity(directory, reserve = 0) {
  const stat = await fs.statfs(directory, { bigint: true });
  requireValue(stat.bavail * stat.bsize - BigInt(reserve) >= MIN_FREE, 'INSUFFICIENT_LOCAL_ARCHIVE_SPACE');
}
function identity(stat) { return [stat.dev, stat.ino, stat.size, stat.mtimeNs, stat.ctimeNs].map(String).join(':'); }
class DigestStream extends Transform {
  constructor(limit = Number.MAX_SAFE_INTEGER, spaceDirectory = null) {
    super(); this.hash = createHash('sha256'); this.bytes = 0; this.limit = limit; this.directory = spaceDirectory; this.nextSpaceCheck = 64 * 1024 ** 2;
  }
  _transform(chunk, _encoding, callback) {
    this.bytes += chunk.length;
    if (!Number.isSafeInteger(this.bytes) || this.bytes > this.limit) { callback(new Error('ARCHIVE_STREAM_SIZE_LIMIT')); return; }
    this.hash.update(chunk);
    if (this.directory && this.bytes >= this.nextSpaceCheck) {
      this.nextSpaceCheck = this.bytes + 64 * 1024 ** 2;
      capacity(this.directory).then(() => callback(null, chunk), callback);
    } else callback(null, chunk);
  }
  result() { return { bytes: this.bytes, sha256: this.hash.digest('hex') }; }
}
function completion(child) {
  return new Promise((resolve, reject) => {
    child.once('error', () => reject(new Error('CHILD_START_FAILED')));
    child.once('close', (code, signal) => code === 0 && !signal ? resolve() : reject(new Error(`CHILD_EXIT_FAILED:${code ?? signal}`)));
  });
}
async function collectPrivateStderr(child, filename) {
  await pipeline(child.stderr, createWriteStream(filename, { flags: 'wx', mode: 0o600 }));
}
function stopChild(child) {
  if (child.exitCode !== null || child.signalCode !== null || child.archiveStopRequested) return;
  child.archiveStopRequested = true; child.kill('SIGTERM');
  const timer = setTimeout(() => { if (child.exitCode === null && child.signalCode === null) child.kill('SIGKILL'); }, 2000);
  timer.unref(); child.once('close', () => clearTimeout(timer));
}
async function allChildStreams(child, promises) {
  try { await Promise.all(promises); }
  catch (error) { stopChild(child); child.stdout.destroy(); if (child.stdin) child.stdin.destroy(); await Promise.allSettled(promises); throw error; }
}
async function closed(handle) { try { await handle.close(); } catch (error) { if (error.code !== 'EBADF') throw error; } }
function noOutput() { return new Writable({ write(_chunk, _encoding, callback) { callback(); } }); }
function metadataCompression(meta) { return meta.header.compression ?? (meta.header.payloadFormat === 'gzip+tar' ? 'gzip' : undefined); }
function requireZstdChecksum() {
  let header = Buffer.alloc(0); let checked = false;
  return new Transform({
    transform(chunk, _encoding, callback) {
      if (checked) { callback(null, chunk); return; }
      header = Buffer.concat([header, chunk]);
      if (header.length < 5) { callback(); return; }
      if (header.readUInt32LE(0) !== 0xfd2fb528 || !(header[4] & 4)) { callback(new Error('ZSTD_FRAME_CHECKSUM_REQUIRED')); return; }
      checked = true; callback(null, header); header = null;
    },
    flush(callback) { callback(checked ? null : new Error('ZSTD_FRAME_HEADER_TRUNCATED')); },
  });
}
function validateMetadata(meta) {
  const compression = metadataCompression(meta);
  requireValue(meta.schema === 1 && meta.status === 'ENCRYPTED_UNVERIFIED' && meta.algorithm === ALGORITHM &&
    ['files', 'docker'].includes(meta.header.kind) && meta.header.schema === 1 && meta.header.algorithm === ALGORITHM &&
    ((compression === 'gzip' && meta.header.payloadFormat === 'gzip+tar') ||
     (compression === 'zstd' && meta.header.payloadFormat === 'zstd+tar')) &&
    meta.archiveId === meta.header.archiveId && /^[a-f0-9-]{36}$/.test(meta.archiveId) &&
    /^[a-f0-9]{24}$/.test(meta.nonce) && /^[a-f0-9]{32}$/.test(meta.authTag) &&
    HEX64.test(meta.ciphertext.sha256) && HEX64.test(meta.compressedPlaintext.sha256) &&
    HEX64.test(meta.header.expectedManifestSha256) &&
    Number.isSafeInteger(meta.ciphertext.bytes) && meta.ciphertext.bytes > 0 &&
    meta.ciphertext.bytes === meta.compressedPlaintext.bytes &&
    typeof meta.ciphertextFile === 'string' && path.basename(meta.ciphertextFile) === meta.ciphertextFile,
  'ARCHIVE_METADATA_INVALID');
}

export async function archive({ commandJson, outputDir, name, kind, expected, keyRoot = DEFAULT_KEYS, maxInputBytes, compression = 'gzip' }) {
  requireValue(/^[A-Za-z0-9][A-Za-z0-9._-]{0,119}$/.test(name) && ['files', 'docker'].includes(kind) && ['gzip', 'zstd'].includes(compression), 'ARCHIVE_ARGUMENT_INVALID');
  if (maxInputBytes !== undefined) requireValue(Number.isSafeInteger(maxInputBytes) && maxInputBytes > 0, 'ARCHIVE_LIMIT_INVALID');
  const directory = await privateDirectory(path.resolve(outputDir));
  const keys = await privateDirectory(path.resolve(keyRoot));
  requireValue(!keys.startsWith(`${directory}${path.sep}`) && keys !== directory && !directory.startsWith(`${keys}${path.sep}`), 'KEY_MUST_BE_SEPARATE_FROM_ARCHIVE');
  const manifestBytes = await readPrivate(path.resolve(expected));
  const commandBytes = await readPrivate(path.resolve(commandJson));
  const args = JSON.parse(commandBytes.toString('utf8'));
  requireValue(Array.isArray(args) && args.length > 0 && args.every(arg => typeof arg === 'string' && !arg.includes('\0')), 'CHILD_ARGV_ARRAY_REQUIRED');
  await capacity(directory, maxInputBytes ?? 0);
  const archiveId = randomUUID();
  const ciphertextFile = `${name}.tar.${compression === 'gzip' ? 'gz' : 'zst'}.aes256gcm`;
  const finalPath = path.join(directory, ciphertextFile);
  const metadataPath = path.join(directory, `${name}.archive.json`);
  for (const target of [finalPath, metadataPath]) {
    try { await fs.lstat(target); throw new Error('ARCHIVE_DESTINATION_EXISTS'); } catch (error) { if (error.code !== 'ENOENT') throw error; }
  }
  const partial = `${finalPath}.partial-${archiveId}`;
  const keyLocator = path.join(keys, `${archiveId}.key`);
  const key = randomBytes(32); const nonce = randomBytes(12);
  await exclusiveWrite(keyLocator, key);
  await syncDirectory(keys);
  const header = { schema: 1, algorithm: ALGORITHM, archiveId, kind, payloadFormat: `${compression}+tar`,
    createdAt: new Date().toISOString(), expectedManifestSha256: createHash('sha256').update(manifestBytes).digest('hex') };
  if (compression === 'zstd') header.compression = 'zstd';
  const cipher = createCipheriv(ALGORITHM, key, nonce); cipher.setAAD(Buffer.from(canonical(header))); key.fill(0);
  const plain = new DigestStream(maxInputBytes); const encrypted = new DigestStream(Number.MAX_SAFE_INTEGER, directory);
  const child = spawn(args[0], args.slice(1), { shell: false, stdio: ['ignore', 'pipe', 'pipe'] });
  const exited = completion(child); exited.catch(() => {});
  const stderr = collectPrivateStderr(child, path.join(directory, `${name}.${archiveId}.stderr.private.log`)); stderr.catch(() => {});
  try {
    const output = createWriteStream(partial, { flags: constants.O_WRONLY | constants.O_CREAT | constants.O_EXCL | constants.O_NOFOLLOW, mode: 0o600 });
    const transfer = pipeline(child.stdout, plain, cipher, encrypted, output);
    await allChildStreams(child, [transfer, exited, stderr]);
    const plaintextResult = plain.result(); const cipherResult = encrypted.result();
    requireValue(cipherResult.bytes > 0, 'EMPTY_ARCHIVE_STREAM');
    const file = await privateFile(partial); try { await file.sync(); } finally { await file.close(); }
    const metadata = { schema: 1, status: 'ENCRYPTED_UNVERIFIED', algorithm: ALGORITHM, archiveId, header,
      ciphertextFile, ciphertext: cipherResult, compressedPlaintext: plaintextResult,
      nonce: nonce.toString('hex'), authTag: cipher.getAuthTag().toString('hex'), keyLocator,
      expectedManifestLocator: path.resolve(expected), childCommandSha256: createHash('sha256').update(commandBytes).digest('hex'), childExitCode: 0 };
    const partialMeta = `${metadataPath}.partial-${archiveId}`;
    await exclusiveWrite(partialMeta, `${JSON.stringify(metadata, null, 2)}\n`);
    await finalizeNoOverwrite(partial, finalPath);
    await finalizeNoOverwrite(partialMeta, metadataPath);
    return { status: 'ENCRYPTED_UNVERIFIED', archiveId, metadataPath, ciphertext: cipherResult };
  } catch (error) {
    stopChild(child);
    await Promise.allSettled([exited, stderr]);
    await exclusiveWrite(path.join(directory, `${name}.${archiveId}.failed.json`), `${JSON.stringify({ schema: 1, status: 'FAILED', archiveId,
      error: error.message, partialCiphertext: path.basename(partial), keyLocator, successfulMetadataCreated: false }, null, 2)}\n`);
    throw error;
  }
}

export async function verify({ metadata: metadataPath, expected, keyFile, python = 'python3' }) {
  const metadataBytes = await readPrivate(path.resolve(metadataPath));
  const meta = JSON.parse(metadataBytes.toString('utf8')); validateMetadata(meta);
  const compression = metadataCompression(meta);
  const directory = await privateDirectory(path.dirname(path.resolve(metadataPath)));
  const manifestPath = path.resolve(expected ?? meta.expectedManifestLocator);
  const expectedBytes = await readPrivate(manifestPath);
  requireValue(createHash('sha256').update(expectedBytes).digest('hex') === meta.header.expectedManifestSha256, 'EXPECTED_MANIFEST_CHANGED');
  const key = await readPrivate(path.resolve(keyFile ?? meta.keyLocator));
  let handle;
  const makeDecipher = () => { const value = createDecipheriv(ALGORITHM, key, Buffer.from(meta.nonce, 'hex'));
    value.setAAD(Buffer.from(canonical(meta.header))); value.setAuthTag(Buffer.from(meta.authTag, 'hex')); return value; };
  const source = () => createReadStream('', { fd: handle.fd, autoClose: false, start: 0, highWaterMark: 1024 * 1024 });
  let child;
  try {
    requireValue(key.length === 32, 'ARCHIVE_KEY_INVALID');
    handle = await privateFile(path.join(directory, meta.ciphertextFile));
    const initial = await handle.stat({ bigint: true });
    requireValue(initial.size === BigInt(meta.ciphertext.bytes), 'CIPHERTEXT_SIZE_MISMATCH');
    // Authenticate before allowing any decompression or TAR parsing of disk bytes.
    const cipherDigest = new DigestStream(meta.ciphertext.bytes); const plainDigest = new DigestStream(meta.compressedPlaintext.bytes);
    await pipeline(source(), cipherDigest, makeDecipher(), plainDigest, noOutput());
    requireValue(canonical(cipherDigest.result()) === canonical(meta.ciphertext) && canonical(plainDigest.result()) === canonical(meta.compressedPlaintext), 'ARCHIVE_DIGEST_MISMATCH');
    requireValue(identity(initial) === identity(await handle.stat({ bigint: true })), 'ARCHIVE_CHANGED_DURING_VERIFY');
    child = spawn(python, [VALIDATOR, '--kind', meta.header.kind, '--expected', manifestPath,
      '--expected-sha256', meta.header.expectedManifestSha256], { shell: false, stdio: ['pipe', 'pipe', 'pipe'] });
    const exited = completion(child); exited.catch(() => {});
    const stderr = collectPrivateStderr(child, path.join(directory, `${meta.archiveId}.verify-${randomUUID()}.stderr.private.log`)); stderr.catch(() => {});
    let response = ''; let outputTooLarge = false;
    child.stdout.on('data', chunk => { if (response.length + chunk.length > 1024 * 1024) { outputTooLarge = true; stopChild(child); return; } response += chunk.toString('utf8'); });
    const secondDigest = new DigestStream(meta.ciphertext.bytes);
    const decompression = compression === 'gzip' ? [createGunzip()] : [requireZstdChecksum(),
      createZstdDecompress({ params: { [zlibConstants.ZSTD_d_windowLogMax]: 29 } })];
    await allChildStreams(child, [pipeline(source(), secondDigest, makeDecipher(), ...decompression, child.stdin), exited, stderr]);
    requireValue(!outputTooLarge, 'VALIDATOR_OUTPUT_LIMIT');
    requireValue(canonical(secondDigest.result()) === canonical(meta.ciphertext) && identity(initial) === identity(await handle.stat({ bigint: true })), 'ARCHIVE_CHANGED_DURING_VERIFY');
    const tar = JSON.parse(response); requireValue(tar.status === 'PASS', 'TAR_VERIFICATION_FAILED');
    const result = { schema: 1, status: 'VERIFIED', verifiedAt: new Date().toISOString(), archiveId: meta.archiveId,
      metadataSha256: createHash('sha256').update(metadataBytes).digest('hex'), ciphertext: meta.ciphertext,
      expectedManifestSha256: meta.header.expectedManifestSha256,
      aesKeySha256: createHash('sha256').update(key).digest('hex'), authenticatedReadPasses: 2, gcmAuthenticated: true,
      compression, gzipCrcVerified: compression === 'gzip', zstdChecksumVerified: compression === 'zstd',
      noExtraction: true, tar };
    const receipt = path.join(directory, `${meta.archiveId}.verified-${randomUUID()}.json`);
    await exclusiveWrite(receipt, `${JSON.stringify(result, null, 2)}\n`);
    return { ...result, verificationReceipt: receipt };
  } finally { key.fill(0); if (child) stopChild(child); if (handle) await closed(handle); }
}

function cliArgs(argv) {
  const [action, ...rest] = argv; const flags = {};
  for (let i = 0; i < rest.length; i += 2) {
    requireValue(rest[i]?.startsWith('--') && typeof rest[i + 1] === 'string', 'ARGUMENT_PAIR_REQUIRED');
    const key = rest[i].slice(2); requireValue(!(key in flags), 'DUPLICATE_ARGUMENT'); flags[key] = rest[i + 1];
  }
  return { action, flags };
}
async function main() {
  const { action, flags } = cliArgs(process.argv.slice(2)); let result;
  if (action === 'archive') result = await archive({ commandJson: flags['command-json'], outputDir: flags['output-dir'], name: flags.name,
    kind: flags.kind, expected: flags.expected, compression: flags.compression ?? 'gzip',
    maxInputBytes: flags['max-input-bytes'] === undefined ? undefined : Number(flags['max-input-bytes']) });
  else if (action === 'verify') result = await verify({ metadata: flags.metadata, expected: flags.expected, keyFile: flags['key-file'] });
  else throw new Error('EXPECTED_ARCHIVE_OR_VERIFY_ACTION');
  process.stdout.write(`${JSON.stringify(result, null, 2)}\n`);
}
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) main().catch(error => {
  // Original child stderr and validator paths are private files, never terminal output.
  const code = /^[A-Z][A-Z0-9_]*(?::[0-9A-Z_-]+)?$/.test(error.message) ? error.message :
    (/^[A-Z][A-Z0-9_]+$/.test(error.code ?? '') ? error.code : 'ARCHIVE_OPERATION_FAILED');
  process.stderr.write(`${JSON.stringify({ status: 'FAILED', error: code })}\n`); process.exitCode = 1;
});
