import test from 'node:test';
import assert from 'node:assert/strict';
import * as fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { createHash, randomBytes } from 'node:crypto';
import { gunzipSync, zstdDecompressSync, constants as zlibConstants } from 'node:zlib';
import { archive, verify } from './backup_archive.mjs';

const root = await fs.mkdtemp(path.join(os.tmpdir(), 'harnes-archive-test-'));
await fs.chmod(root, 0o700);
const fixtures = spawnSync('python3', ['-c', String.raw`
import io,tarfile,gzip,hashlib,json,os,sys
p=sys.argv[1]
def sha(b):return hashlib.sha256(b).hexdigest()
def put(n,b):
 with open(os.path.join(p,n),'wb') as f:f.write(b)
 os.chmod(os.path.join(p,n),0o600)
def canon(v):return json.dumps(v,ensure_ascii=False,sort_keys=True,separators=(',',':')).encode()
data=(b'synthetic-private-content-'*10000)
entries=[];out=io.BytesIO()
with tarfile.open(fileobj=out,mode='w',format=tarfile.PAX_FORMAT) as t:
 for n,kind in [('opt','dir'),('opt/原始','file'),('opt/hard','file'),('opt/link','symlink')]:
  m=tarfile.TarInfo(n);m.mode=0o755 if kind=='dir' else 0o640;m.uid=12;m.gid=34;m.mtime=1700000000;m.pax_headers={'mtime':'1700000000.123456789'}
  e={'path':n,'type':kind,'size':len(data) if kind=='file' else 0,'mode':m.mode,'uid':12,'gid':34,'mtime_ns':'1700000000123456789'}
  if kind=='dir':m.type=tarfile.DIRTYPE
  elif kind=='symlink':m.type=tarfile.SYMTYPE;m.linkname='原始';e['link']='原始'
  else:
   e['sha256']=sha(data)
   if n.endswith('hard'):m.type=tarfile.LNKTYPE;m.linkname='opt/原始'
   else:m.size=len(data)
  t.addfile(m,io.BytesIO(data) if m.isreg() else None);entries.append(e)
 end=t.offset
manifest={'schema':1,'entries':entries,'tree_sha256':sha(canon(entries))}
put('files.manifest.json',canon(manifest));put('files.gz',gzip.compress(out.getvalue()))
put('no-end.gz',gzip.compress(out.getvalue()[:end]))
put('bad-gzip.gz',gzip.compress(out.getvalue())[:-7])
broken=io.BytesIO()
with tarfile.open(fileobj=broken,mode='w',format=tarfile.PAX_FORMAT) as t:
 m=tarfile.TarInfo('opt');m.type=tarfile.DIRTYPE;m.mode=0o755;m.uid=12;m.gid=34;m.pax_headers={'mtime':'1700000000.123456789'};t.addfile(m)
 m=tarfile.TarInfo('opt/原始');m.type=tarfile.LNKTYPE;m.linkname='opt/hard';m.mode=0o640;m.uid=12;m.gid=34;m.pax_headers={'mtime':'1700000000.123456789'};t.addfile(m)
put('bad-hardlink.gz',gzip.compress(broken.getvalue()))
manifest['entries'][1]['sha256']='0'*64;manifest['tree_sha256']=sha(canon(entries))
put('wrong-content.manifest.json',canon(manifest))
layer=b'synthetic layer tar bytes'*100
config=canon({'architecture':'amd64','os':'linux','rootfs':{'type':'layers','diff_ids':['sha256:'+sha(layer)]}})
manifest=canon([{'Config':'blobs/sha256/'+sha(config),'Layers':['blobs/sha256/'+sha(layer)],'RepoTags':['local/test:old']}])
oci=canon({'schemaVersion':2,'config':{'digest':'sha256:'+sha(config),'size':len(config)},'layers':[{'digest':'sha256:'+sha(layer),'size':len(layer)}]})
index=canon({'schemaVersion':2,'manifests':[{'digest':'sha256:'+sha(oci),'size':len(oci)}]})
for bad in [False,True]:
 b=io.BytesIO()
 with tarfile.open(fileobj=b,mode='w',format=tarfile.PAX_FORMAT) as t:
  for n,v in [('manifest.json',manifest),('index.json',index),('blobs/sha256/'+sha(oci),oci),('blobs/sha256/'+sha(config),config),('blobs/sha256/'+('0'*64 if bad else sha(layer)),layer)]:
   m=tarfile.TarInfo(n);m.size=len(v);t.addfile(m,io.BytesIO(v))
 put('docker-bad.gz' if bad else 'docker.gz',gzip.compress(b.getvalue()))
put('docker.manifest.json',canon({'schema':1,'image_ids':['sha256:'+sha(config)]}))
manifest_type='application/vnd.oci.image.manifest.v1+json'
index_type='application/vnd.oci.image.index.v1+json'
blobs={};images=[]
def blob(value):
 digest=sha(value);blobs['blobs/sha256/'+digest]=value;return 'sha256:'+digest
def desc(value,kind):return {'digest':blob(value),'size':len(value),'mediaType':kind}
for letter in ['a','b']:
 payload=('modern layer '+letter).encode()
 cfg=canon({'architecture':'amd64','os':'linux','rootfs':{'type':'layers','diff_ids':['sha256:'+sha(payload)]}})
 cfg_desc=desc(cfg,'application/vnd.oci.image.config.v1+json')
 layer_desc=desc(payload,'application/vnd.oci.image.layer.v1.tar')
 doc=canon({'schemaVersion':2,'mediaType':manifest_type,'config':cfg_desc,'layers':[layer_desc]})
 root_desc=desc(doc,manifest_type)
 images.append({'config_id':cfg_desc['digest'],'root':root_desc,'legacy':{'Config':'blobs/sha256/'+sha(cfg),'Layers':['blobs/sha256/'+sha(payload)],'RepoTags':['local/modern:'+letter]}})
inner=canon({'schemaVersion':2,'mediaType':index_type,'manifests':[image['root'] for image in images]})
index_root=desc(inner,index_type)
def docker_fixture(label,roots,legacy,expected):
 values=dict(blobs)
 if roots is not None:values['index.json']=canon({'schemaVersion':2,'mediaType':index_type,'manifests':roots})
 if legacy is not None:values['manifest.json']=canon(legacy)
 b=io.BytesIO()
 with tarfile.open(fileobj=b,mode='w',format=tarfile.PAX_FORMAT) as t:
  for n,value in values.items():
   m=tarfile.TarInfo(n);m.size=len(value);t.addfile(m,io.BytesIO(value))
 put('docker-'+label+'.gz',gzip.compress(b.getvalue()))
 put('docker-'+label+'.json',canon({'schema':1,'image_ids':expected,'repo_tags':['local/modern:a','local/modern:b']}))
roots=[image['root'] for image in images];legacy=[image['legacy'] for image in images]
docker_fixture('modern-manifest',roots,legacy,[item['digest'] for item in roots])
docker_fixture('modern-index',[index_root],legacy,[index_root['digest']])
shared=[dict(image,RepoTags=['local/modern:multi']) for image in legacy]
docker_fixture('modern-index-shared-tag',[index_root],shared,[index_root['digest']])
put('docker-modern-index-shared-tag.json',canon({'schema':1,'image_ids':[index_root['digest']],'repo_tags':['local/modern:multi']}))
docker_fixture('legacy-only',None,legacy,[image['config_id'] for image in images])
docker_fixture('config-scheme',roots,legacy,[image['config_id'] for image in images])
docker_fixture('wrong-root',roots,legacy,['sha256:'+'0'*64])
docker_fixture('missing-root',roots,legacy,[roots[0]['digest']])
docker_fixture('mixed-identities',roots,legacy,[roots[0]['digest'],images[1]['config_id']])
docker_fixture('index-child-root',[index_root],legacy,[roots[0]['digest']])
docker_fixture('inconsistent-catalogs',roots,legacy[:1],[item['digest'] for item in roots])
docker_fixture('wrong-tag',roots,legacy,[item['digest'] for item in roots])
put('docker-wrong-tag.json',canon({'schema':1,'image_ids':[item['digest'] for item in roots],'repo_tags':['local/modern:a','local/modern:wrong']}))
`, root], { encoding: 'utf8' });
assert.equal(fixtures.status, 0, fixtures.stderr);
let sequence = 0;
async function makeArchive({ payload = 'files.gz', expected = 'files.manifest.json', kind = 'files', code = 0, compression } = {}) {
  const name = `case-${++sequence}`;
  const command = path.join(root, `${name}.argv.json`);
  const source = path.join(root, payload);
  await fs.writeFile(command, JSON.stringify([process.execPath, '-e',
    `const fs=require('node:fs');fs.createReadStream(process.argv[1]).pipe(process.stdout);process.exitCode=Number(process.argv[2]);`, source, String(code)]), { mode: 0o600 });
  return archive({ commandJson: command, outputDir: path.join(root, 'archives'), name, kind,
    expected: path.join(root, expected), keyRoot: path.join(root, 'keys'), compression });
}
async function metaFor(result) { return JSON.parse(await fs.readFile(result.metadataPath)); }

test('streamed files archive authenticates and validates unicode, nanoseconds, symlink and hardlink without extraction', async () => {
  const result = await makeArchive();
  const meta = await metaFor(result);
  assert.equal(result.status, 'ENCRYPTED_UNVERIFIED');
  const receipt = await verify({ metadata: result.metadataPath });
  assert.equal(receipt.status, 'VERIFIED');
  assert.equal(receipt.authenticatedReadPasses, 2);
  assert.equal(receipt.tar.entry_count, 4);
  assert.equal(receipt.tar.hardlinks, 1);
  assert.equal(receipt.noExtraction, true);
  assert.equal((await fs.stat(path.dirname(result.metadataPath))).mode & 0o777, 0o700);
  for (const file of [result.metadataPath, meta.keyLocator, path.join(path.dirname(result.metadataPath), meta.ciphertextFile)]) {
    assert.equal((await fs.stat(file)).mode & 0o777, 0o600);
  }
  assert.ok(!meta.keyLocator.startsWith(path.dirname(result.metadataPath) + path.sep));
  assert.ok(!(await fs.readFile(path.join(path.dirname(result.metadataPath), meta.ciphertextFile))).includes(Buffer.from('synthetic-private-content')));
  await assert.rejects(fs.stat(path.join(path.dirname(result.metadataPath), 'opt')), { code: 'ENOENT' });
});

test('ciphertext byte tamper and truncation never produce verification receipt', async () => {
  for (const truncate of [false, true]) {
    const result = await makeArchive(); const meta = await metaFor(result);
    const filename = path.join(path.dirname(result.metadataPath), meta.ciphertextFile);
    const bytes = await fs.readFile(filename);
    if (truncate) await fs.truncate(filename, bytes.length - 1);
    else { bytes[3] ^= 0x80; await fs.writeFile(filename, bytes); }
    await assert.rejects(verify({ metadata: result.metadataPath }));
    assert.equal((await fs.readdir(path.dirname(result.metadataPath))).filter(value => value.startsWith(meta.archiveId + '.verified-')).length, 0);
  }
});

test('private verification proof binds the actual AES key used by both GCM passes', async () => {
  const result = await makeArchive();
  const meta = await metaFor(result);
  const key = await fs.readFile(meta.keyLocator);
  const receipt = await verify({ metadata: result.metadataPath });
  const proof = JSON.parse(await fs.readFile(receipt.verificationReceipt));
  assert.equal(proof.aesKeySha256, createHash('sha256').update(key).digest('hex'));
  const replacement = randomBytes(32);
  await fs.writeFile(meta.keyLocator, replacement);
  assert.notEqual(proof.aesKeySha256, createHash('sha256').update(replacement).digest('hex'));
  await assert.rejects(verify({ metadata: result.metadataPath }));
});

test('nonzero child fails after complete stdout and never publishes archive metadata', async () => {
  const before = new Set(await fs.readdir(path.join(root, 'archives')));
  await assert.rejects(makeArchive({ code: 7 }), /CHILD_EXIT_FAILED:7/);
  const added = (await fs.readdir(path.join(root, 'archives'))).filter(value => !before.has(value));
  assert.equal(added.filter(value => value.endsWith('.archive.json')).length, 0);
  assert.equal(added.filter(value => value.endsWith('.failed.json')).length, 1);
  assert.equal(added.filter(value => value.endsWith('.aes256gcm')).length, 0);
});

test('authenticated but truncated gzip, missing TAR footer and wrong expected content all fail', async () => {
  for (const options of [{ payload: 'bad-gzip.gz' }, { payload: 'no-end.gz' }, { payload: 'bad-hardlink.gz' }, { expected: 'wrong-content.manifest.json' }]) {
    const result = await makeArchive(options);
    await assert.rejects(verify({ metadata: result.metadataPath }));
  }
});

test('private argv parse failures never echo secrets into CLI stderr', async () => {
  const filename = path.join(root, 'invalid.argv.json');
  await fs.writeFile(filename, '[synthetic-sensitive-argument', { mode: 0o600 });
  const child = spawnSync(process.execPath, [fileURLToPath(new URL('./backup_archive.mjs', import.meta.url)), 'archive',
    '--command-json', filename, '--output-dir', path.join(root, 'archives'), '--name', 'invalid-json',
    '--kind', 'files', '--expected', path.join(root, 'files.manifest.json')], { encoding: 'utf8' });
  assert.equal(child.status, 1);
  assert.ok(!child.stderr.includes('synthetic-sensitive-argument'));
  assert.equal(JSON.parse(child.stderr).error, 'ARCHIVE_OPERATION_FAILED');
});

test('manifest relocation is permitted only for byte-identical manifest', async () => {
  const result = await makeArchive();
  const relocated = path.join(root, 'relocated.manifest.json');
  await fs.copyFile(path.join(root, 'files.manifest.json'), relocated);
  await fs.chmod(relocated, 0o600);
  assert.equal((await verify({ metadata: result.metadataPath, expected: relocated })).status, 'VERIFIED');
  await fs.appendFile(relocated, '\n');
  await assert.rejects(verify({ metadata: result.metadataPath, expected: relocated }), /EXPECTED_MANIFEST_CHANGED/);
});

test('expected manifest is bound again when the TAR validator opens it', async () => {
  const expected = 'race.manifest.json';
  await fs.copyFile(path.join(root, 'files.manifest.json'), path.join(root, expected));
  await fs.chmod(path.join(root, expected), 0o600);
  const result = await makeArchive({ expected });
  const wrapper = path.join(root, 'change-manifest-python');
  await fs.writeFile(wrapper, `#!/usr/bin/env node\nconst fs=require('node:fs');const a=process.argv.slice(2);fs.appendFileSync(a[a.indexOf('--expected')+1],'\\n');const r=require('node:child_process').spawnSync('python3',a,{stdio:'inherit'});process.exit(r.status??1);\n`, { mode: 0o700 });
  await assert.rejects(verify({ metadata: result.metadataPath, python: wrapper }));
});

test('Docker save validates all blobs, image configs, layers and OCI descriptors; incorrect blob refuses', async () => {
  const result = await makeArchive({ payload: 'docker.gz', expected: 'docker.manifest.json', kind: 'docker' });
  const receipt = await verify({ metadata: result.metadataPath });
  assert.equal(receipt.tar.image_count, 1);
  assert.equal(receipt.tar.blob_count, 3);
  const bad = await makeArchive({ payload: 'docker-bad.gz', expected: 'docker.manifest.json', kind: 'docker' });
  await assert.rejects(verify({ metadata: bad.metadataPath }));
});

test('Docker29 manifest/index root identities and legacy/config identities require exact complete schemes', async () => {
  for (const [label, identity] of [['modern-manifest', 'oci_root_digest'], ['modern-index', 'oci_root_digest'], ['modern-index-shared-tag', 'oci_root_digest'],
    ['legacy-only', 'config_digest'], ['config-scheme', 'config_digest']]) {
    const result = await makeArchive({ payload: `docker-${label}.gz`, expected: `docker-${label}.json`, kind: 'docker' });
    const receipt = await verify({ metadata: result.metadataPath });
    const expected = JSON.parse(await fs.readFile(path.join(root, `docker-${label}.json`)));
    assert.equal(receipt.tar.identity_kind, identity);
    assert.deepEqual(receipt.tar.image_ids, expected.image_ids.sort());
    assert.deepEqual(receipt.tar.repo_tags, expected.repo_tags.sort());
  }
  for (const label of ['wrong-root', 'missing-root', 'mixed-identities', 'index-child-root', 'inconsistent-catalogs', 'wrong-tag']) {
    const result = await makeArchive({ payload: `docker-${label}.gz`, expected: `docker-${label}.json`, kind: 'docker' });
    await assert.rejects(verify({ metadata: result.metadataPath }), label);
  }
});

test('real zstd --long=29 checksum stream verifies; codec tamper and oversized windows refuse', async () => {
  const tar = gunzipSync(await fs.readFile(path.join(root, 'files.gz')));
  const compressed = spawnSync('zstd', ['-1', '-T1', '--long=29', '--check', '-c'], { input: tar, maxBuffer: 4 * 1024 ** 2 });
  assert.equal(compressed.status, 0, compressed.stderr.toString());
  assert.equal(compressed.stdout.readUInt32LE(0), 0xfd2fb528);
  assert.equal(compressed.stdout[4] & 0x20, 0, 'real streaming frame has an explicit window descriptor');
  assert.equal(10 + (compressed.stdout[5] >> 3), 29);
  assert.throws(() => zstdDecompressSync(compressed.stdout, { params: { [zlibConstants.ZSTD_d_windowLogMax]: 27 } }));
  await fs.writeFile(path.join(root, 'long.zst'), compressed.stdout, { mode: 0o600 });
  const result = await makeArchive({ payload: 'long.zst', compression: 'zstd' });
  const meta = await metaFor(result);
  assert.equal(meta.header.payloadFormat, 'zstd+tar');
  assert.equal(meta.header.compression, 'zstd');
  const receipt = await verify({ metadata: result.metadataPath });
  assert.equal(receipt.status, 'VERIFIED');
  assert.equal(receipt.compression, 'zstd');
  assert.equal(receipt.gzipCrcVerified, false);
  assert.equal(receipt.zstdChecksumVerified, true);
  meta.header.payloadFormat = 'gzip+tar'; delete meta.header.compression;
  const tampered = path.join(path.dirname(result.metadataPath), 'codec-tamper.archive.json');
  await fs.writeFile(tampered, JSON.stringify(meta), { mode: 0o600 });
  await assert.rejects(verify({ metadata: tampered }));
  const tooLarge = Buffer.from(compressed.stdout); tooLarge[5] = (30 - 10) << 3;
  await fs.writeFile(path.join(root, 'too-large.zst'), tooLarge, { mode: 0o600 });
  const oversized = await makeArchive({ payload: 'too-large.zst', compression: 'zstd' });
  await assert.rejects(verify({ metadata: oversized.metadataPath }));
});

test.after(async () => { await fs.rm(root, { recursive: true, force: true }); });
