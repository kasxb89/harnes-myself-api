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
// Run after the current frontend build and before Go embeds web/dist. This is
// an explicit build-time operation, never a server startup or deploy hook.
import { createHash } from 'node:crypto'
import {
  existsSync,
  lstatSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  realpathSync,
  writeFileSync,
} from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { parseArgs } from 'node:util'

function sha256(body) {
  return createHash('sha256').update(body).digest('hex')
}

function assetPath(value) {
  if (
    typeof value !== 'string' ||
    !value ||
    path.posix.isAbsolute(value) ||
    path.win32.isAbsolute(value) ||
    /[\\%?#]/.test(value) ||
    [...value].some(
      (char) => char.charCodeAt(0) < 32 || char.charCodeAt(0) === 127
    ) ||
    path.posix.normalize(value) !== value ||
    value === '..' ||
    value.startsWith('../') ||
    value.split('/').some((part) => part.startsWith('.'))
  ) {
    throw new Error('Invalid retained asset path')
  }
  return value
}

function directory(value) {
  if (!lstatSync(value).isDirectory() || lstatSync(value).isSymbolicLink()) {
    throw new Error('Asset root must be a regular directory')
  }
  return realpathSync(value)
}

function inside(root, value) {
  const relative = path.relative(root, value)
  return (
    relative === '' ||
    (!relative.startsWith(`..${path.sep}`) &&
      relative !== '..' &&
      !path.isAbsolute(relative))
  )
}

function inventory(root, relative = '') {
  const files = []
  const entries = readdirSync(path.join(root, relative), {
    withFileTypes: true,
  })
  for (const entry of entries) {
    const name = relative ? `${relative}/${entry.name}` : entry.name
    assetPath(name)
    if (entry.isDirectory()) {
      files.push(...inventory(root, name))
    } else if (entry.isFile()) {
      const body = readFileSync(path.join(root, name))
      files.push({ path: name, bytes: body.length, sha256: sha256(body) })
    } else {
      throw new Error('Asset trees cannot contain symlinks or special files')
    }
  }
  return files.sort((a, b) => (a.path < b.path ? -1 : Number(a.path > b.path)))
}

/** Verify both inputs completely before adding any file; never overwrite one. */
export function retainStaticAssets(options) {
  const current = directory(options.currentDist)
  const prior = directory(options.priorDist)
  const requestedOutput = path.resolve(options.outputManifest)
  const output = path.join(
    directory(path.dirname(requestedOutput)),
    path.basename(requestedOutput)
  )
  if (inside(current, prior) || inside(prior, current)) {
    throw new Error('Current and prior asset roots must be independent')
  }
  if (inside(current, output) || inside(prior, output) || existsSync(output)) {
    throw new Error('A new output manifest must be outside both asset trees')
  }
  const manifestBytes = readFileSync(options.priorManifest)
  const manifest = JSON.parse(manifestBytes.toString('utf8'))
  if (!Array.isArray(manifest.copied_files) || !manifest.copied_files.length) {
    throw new Error('Expected a complete copied_files source manifest')
  }
  const expected = new Map()
  for (const row of manifest.copied_files) {
    const name = assetPath(row.path)
    if (
      !Number.isSafeInteger(row.bytes) ||
      row.bytes < 0 ||
      !/^[0-9a-f]{64}$/.test(row.sha256) ||
      expected.has(name)
    ) {
      throw new Error('Invalid or duplicate source manifest entry')
    }
    expected.set(name, row)
  }
  const priorFiles = inventory(prior)
  if (
    priorFiles.length !== expected.size ||
    priorFiles.some((row) => {
      const recorded = expected.get(row.path)
      return (
        !recorded ||
        row.bytes !== recorded.bytes ||
        row.sha256 !== recorded.sha256
      )
    })
  ) {
    throw new Error(
      'Prior asset tree differs from its complete source manifest'
    )
  }
  const currentFiles = inventory(current)
  const currentByPath = new Map(currentFiles.map((row) => [row.path, row]))
  const index = currentByPath.get('index.html')
  if (!index) throw new Error('Current build index.html is required')
  const selected = priorFiles.filter((row) => row.path.startsWith('static/'))
  if (!selected.length) {
    throw new Error('Prior manifest contains no static assets')
  }
  if (selected.some((row) => /\.html?$/i.test(row.path))) {
    throw new Error('HTML is not a retained static asset')
  }
  const additions = []
  const identical = []
  // Hold the verified bytes until writing, so later reads of a changed source
  // cannot silently change the published content. Source trees stay read-only.
  for (const row of selected) {
    const existing = currentByPath.get(row.path)
    if (existing) {
      if (existing.bytes !== row.bytes || existing.sha256 !== row.sha256) {
        throw new Error(`Conflicting immutable asset path: ${row.path}`)
      }
      identical.push(row.path)
    } else {
      const target = path.join(current, row.path)
      if (existsSync(target)) {
        throw new Error(`Asset target path is not a file: ${row.path}`)
      }
      let parent = path.dirname(target)
      while (parent !== current) {
        if (existsSync(parent) && !lstatSync(parent).isDirectory()) {
          throw new Error(`Asset target parent is not a directory: ${row.path}`)
        }
        parent = path.dirname(parent)
      }
      const body = readFileSync(path.join(prior, row.path))
      if (body.length !== row.bytes || sha256(body) !== row.sha256) {
        throw new Error(`Prior asset changed during validation: ${row.path}`)
      }
      additions.push({ row, body })
    }
  }
  for (const addition of additions) {
    const target = path.join(current, addition.row.path)
    mkdirSync(path.dirname(target), { recursive: true })
    writeFileSync(target, addition.body, { flag: 'wx', mode: 0o644 })
  }
  const unionFiles = inventory(current)
  const unionByPath = new Map(unionFiles.map((row) => [row.path, row]))
  for (const row of [...currentFiles, ...selected]) {
    const actual = unionByPath.get(row.path)
    if (!actual || actual.bytes !== row.bytes || actual.sha256 !== row.sha256) {
      throw new Error(`Output verification failed: ${row.path}`)
    }
  }
  const result = {
    schema_version: 1,
    status: 'VERIFIED_STATIC_UNION_NO_OVERWRITES',
    current: {
      root: current,
      files: currentFiles,
      files_sha256: sha256(JSON.stringify(currentFiles)),
    },
    prior: {
      root: prior,
      manifest_path: path.resolve(options.priorManifest),
      manifest_sha256: sha256(manifestBytes),
      prior_source: manifest.prior_source ?? null,
      files: priorFiles,
      selected_static_files: selected.length,
      excluded_files: priorFiles.filter(
        (row) => !row.path.startsWith('static/')
      ),
    },
    added_files: additions.map((entry) => entry.row.path),
    identical_files: identical,
    current_index_sha256: index.sha256,
    output: {
      root: current,
      files: unionFiles,
      files_sha256: sha256(JSON.stringify(unionFiles)),
    },
  }
  writeFileSync(output, `${JSON.stringify(result, null, 2)}\n`, {
    flag: 'wx',
    mode: 0o600,
  })
  return result
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  try {
    const { values } = parseArgs({
      options: {
        current: { type: 'string' },
        prior: { type: 'string' },
        'prior-manifest': { type: 'string' },
        'output-manifest': { type: 'string' },
      },
    })
    if (Object.keys(values).length !== 4) {
      throw new Error(
        'Required: --current --prior --prior-manifest --output-manifest'
      )
    }
    const result = retainStaticAssets({
      currentDist: values.current,
      priorDist: values.prior,
      priorManifest: values['prior-manifest'],
      outputManifest: values['output-manifest'],
    })
    console.log(
      JSON.stringify({
        status: result.status,
        added: result.added_files.length,
        identical: result.identical_files.length,
        files: result.output.files.length,
        output_files_sha256: result.output.files_sha256,
      })
    )
  } catch (error) {
    console.error(
      error instanceof Error ? error.message : 'Static asset retention failed'
    )
    process.exitCode = 1
  }
}
