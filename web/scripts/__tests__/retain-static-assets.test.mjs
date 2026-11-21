/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  statSync,
  symlinkSync,
  writeFileSync,
} from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'

import { retainStaticAssets } from '../retain-static-assets.mjs'

function fixture(t, priorFiles, currentFiles = {}) {
  const root = mkdtempSync(path.join(os.tmpdir(), 'static-union-test-'))
  t.after(() => rmSync(root, { recursive: true, force: true }))
  const current = path.join(root, 'current')
  const prior = path.join(root, 'prior')
  mkdirSync(current)
  mkdirSync(prior)
  const originals = { 'index.html': '<html>current</html>', ...currentFiles }
  const old = {
    'index.html': '<html>old</html>',
    'logo.png': 'old root logo',
    ...priorFiles,
  }
  for (const [directory, files] of [
    [current, originals],
    [prior, old],
  ]) {
    for (const [name, body] of Object.entries(files)) {
      const target = path.join(directory, name)
      mkdirSync(path.dirname(target), { recursive: true })
      writeFileSync(target, body)
    }
  }
  const manifest = {
    prior_source: 'retained-original-build',
    copied_files: Object.entries(old).map(([name, body]) => ({
      path: name,
      bytes: Buffer.byteLength(body),
      sha256: createHash('sha256').update(body).digest('hex'),
    })),
  }
  const manifestPath = path.join(root, 'prior-manifest.json')
  writeFileSync(manifestPath, JSON.stringify(manifest))
  const options = {
    currentDist: current,
    priorDist: prior,
    priorManifest: manifestPath,
    outputManifest: path.join(root, 'proof.json'),
  }
  return { root, current, prior, originals, old, manifest, options }
}

test('retains the complete static dependency files, preserving current index and identical files', (t) => {
  const files = {
    'static/js/async/route.aaaaaaaaaa.js': 'old lazy route',
    'static/css/async/route.bbbbbbbbbb.css': 'old lazy css',
    'static/js/async/route.aaaaaaaaaa.js.LICENSE.txt': 'license attribution',
    'static/font/shared.cccccccccc.woff2': 'shared font',
  }
  const f = fixture(t, files, {
    'static/js/current.dddddddddd.js': 'current runtime',
    'static/font/shared.cccccccccc.woff2': 'shared font',
  })
  const font = path.join(f.current, 'static/font/shared.cccccccccc.woff2')
  const originalInode = statSync(font).ino
  const result = retainStaticAssets(f.options)
  for (const [name, body] of Object.entries(files)) {
    assert.equal(readFileSync(path.join(f.current, name), 'utf8'), body)
    assert.equal(readFileSync(path.join(f.prior, name), 'utf8'), body)
  }
  for (const [name, body] of Object.entries(f.originals)) {
    assert.equal(readFileSync(path.join(f.current, name), 'utf8'), body)
  }
  assert.equal(existsSync(path.join(f.current, 'logo.png')), false)
  assert.equal(statSync(font).ino, originalInode)
  assert.equal(result.added_files.length, 3)
  assert.deepEqual(result.identical_files, [
    'static/font/shared.cccccccccc.woff2',
  ])
  assert.deepEqual(
    result.prior.excluded_files.map((row) => row.path),
    ['index.html', 'logo.png']
  )
  assert.equal(result.output.files.length, 6)
  const proof = JSON.parse(readFileSync(f.options.outputManifest, 'utf8'))
  assert.equal(proof.current.files.length, 3)
  assert.equal(proof.prior.files.length, 6)
  assert.equal(proof.output.files.length, 6)
  assert.equal(
    proof.prior.manifest_sha256,
    createHash('sha256')
      .update(readFileSync(f.options.priorManifest))
      .digest('hex')
  )
})

test('same-path different bytes fail before any earlier sorted addition is written', (t) => {
  const f = fixture(
    t,
    {
      'static/js/a.aaaaaaaaaa.js': 'add this only after all checks',
      'static/js/z.bbbbbbbbbb.js': 'old conflicting bytes',
    },
    { 'static/js/z.bbbbbbbbbb.js': 'current bytes' }
  )
  assert.throws(
    () => retainStaticAssets(f.options),
    /Conflicting immutable asset path/
  )
  assert.equal(
    existsSync(path.join(f.current, 'static/js/a.aaaaaaaaaa.js')),
    false
  )
  assert.equal(
    readFileSync(path.join(f.current, 'static/js/z.bbbbbbbbbb.js'), 'utf8'),
    'current bytes'
  )
  assert.equal(
    readFileSync(path.join(f.current, 'index.html'), 'utf8'),
    f.originals['index.html']
  )
  assert.equal(existsSync(f.options.outputManifest), false)
})

test('unsafe and duplicate manifest paths are rejected without adding a file', (t) => {
  const f = fixture(t, { 'static/js/old.aaaaaaaaaa.js': 'old' })
  const original = f.manifest.copied_files.at(-1)
  for (const invalid of [
    '../outside.js',
    '/absolute.js',
    'static/../secret.js',
    'static\\outside.js',
    'static/%2e%2e/secret.js',
    'static/.env',
    'C:/outside.js',
  ]) {
    writeFileSync(
      f.options.priorManifest,
      JSON.stringify({ copied_files: [{ ...original, path: invalid }] })
    )
    assert.throws(
      () => retainStaticAssets(f.options),
      /Invalid retained asset path/
    )
  }
  writeFileSync(
    f.options.priorManifest,
    JSON.stringify({ copied_files: [original, original] })
  )
  assert.throws(
    () => retainStaticAssets(f.options),
    /duplicate source manifest entry/
  )
  assert.equal(existsSync(path.join(f.current, 'static')), false)
  assert.equal(existsSync(f.options.outputManifest), false)
})

test('source mismatch, missing entry and undeclared extra entry fail complete input verification', (t) => {
  const f = fixture(t, { 'static/js/old.aaaaaaaaaa.js': 'old' })
  writeFileSync(path.join(f.prior, 'index.html'), 'changed excluded root file')
  assert.throws(
    () => retainStaticAssets(f.options),
    /differs from its complete source manifest/
  )
  writeFileSync(path.join(f.prior, 'index.html'), f.old['index.html'])
  writeFileSync(
    path.join(f.prior, 'static/js/extra.bbbbbbbbbb.js'),
    'unrecorded'
  )
  assert.throws(
    () => retainStaticAssets(f.options),
    /differs from its complete source manifest/
  )
  rmSync(path.join(f.prior, 'static/js/extra.bbbbbbbbbb.js'))
  rmSync(path.join(f.prior, 'static/js/old.aaaaaaaaaa.js'))
  assert.throws(
    () => retainStaticAssets(f.options),
    /differs from its complete source manifest/
  )
  assert.equal(existsSync(path.join(f.current, 'static')), false)
})

test('symlink inputs and target parents that are files fail before additions', (t) => {
  const f = fixture(
    t,
    { 'static/js/old.aaaaaaaaaa.js': 'old' },
    { 'static/js': 'not a directory' }
  )
  assert.throws(
    () => retainStaticAssets(f.options),
    /target parent is not a directory/
  )
  assert.equal(
    readFileSync(path.join(f.current, 'static/js'), 'utf8'),
    'not a directory'
  )
  rmSync(path.join(f.current, 'static/js'))
  symlinkSync(
    path.join(f.prior, 'static/js'),
    path.join(f.current, 'static/js')
  )
  assert.throws(
    () => retainStaticAssets(f.options),
    /symlinks or special files/
  )
  assert.equal(existsSync(f.options.outputManifest), false)
})

test('overlapping roots and manifests inside the asset trees are rejected', (t) => {
  const f = fixture(t, { 'static/js/old.aaaaaaaaaa.js': 'old' })
  assert.throws(
    () => retainStaticAssets({ ...f.options, priorDist: f.current }),
    /must be independent/
  )
  assert.throws(
    () =>
      retainStaticAssets({
        ...f.options,
        outputManifest: path.join(f.current, 'proof.json'),
      }),
    /outside both asset trees/
  )
  assert.equal(existsSync(path.join(f.current, 'static')), false)
})

test('old HTML inside static is refused, not copied as a public asset', (t) => {
  const f = fixture(t, { 'static/page.html': '<html>old page</html>' })
  assert.throws(
    () => retainStaticAssets(f.options),
    /HTML is not a retained static asset/
  )
  assert.equal(existsSync(path.join(f.current, 'static')), false)
})

test('the explicit CLI creates a verifiable union and a later invocation does not rewrite existing assets', (t) => {
  const f = fixture(t, { 'static/js/old.aaaaaaaaaa.js': 'old' })
  const script = fileURLToPath(
    new URL('../retain-static-assets.mjs', import.meta.url)
  )
  const result = spawnSync(
    process.execPath,
    [
      script,
      '--current',
      f.current,
      '--prior',
      f.prior,
      '--prior-manifest',
      f.options.priorManifest,
      '--output-manifest',
      f.options.outputManifest,
    ],
    { encoding: 'utf8' }
  )
  assert.equal(result.status, 0, result.stderr)
  assert.equal(JSON.parse(result.stdout).added, 1)
  const inode = statSync(
    path.join(f.current, 'static/js/old.aaaaaaaaaa.js')
  ).ino
  assert.throws(() => retainStaticAssets(f.options), /new output manifest/)
  const second = retainStaticAssets({
    ...f.options,
    outputManifest: path.join(f.root, 'proof-second.json'),
  })
  assert.equal(second.added_files.length, 0)
  assert.equal(
    statSync(path.join(f.current, 'static/js/old.aaaaaaaaaa.js')).ino,
    inode
  )
})
