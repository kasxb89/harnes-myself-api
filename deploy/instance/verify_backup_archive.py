#!/usr/bin/env python3
"""Validate a TAR stream against a private manifest, without extracting files."""
import argparse
import decimal
import hashlib
import json
import posixpath
import re
import sys
import tarfile
import zlib

CHUNK = 1024 * 1024
HEX = re.compile(r"^[a-f0-9]{64}$")
BLOB = re.compile(r"^blobs/sha256/([a-f0-9]{64})$")


def check(condition, code):
    if not condition:
        raise ValueError(code)


def canonical(value):
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode("utf-8")


def name(value, directory=False):
    check(isinstance(value, str) and value and "\0" not in value and not value.startswith("/"), "TAR_PATH_INVALID")
    while value.startswith("./"):
        value = value[2:]
    if directory:
        value = value.rstrip("/")
    check(value and all(part not in ("", ".", "..") for part in value.split("/")), "TAR_PATH_INVALID")
    return value


def member_type(member):
    if member.isreg() or member.islnk():
        return "file"
    if member.isdir():
        return "dir"
    if member.issym():
        return "symlink"
    raise ValueError("TAR_ENTRY_TYPE_UNSUPPORTED")


def metadata_matches(member, expected):
    check(member_type(member) == expected["type"], "TAR_ENTRY_TYPE_MISMATCH")
    for attribute in ("uid", "gid"):
        check(getattr(member, attribute) == expected[attribute], "TAR_OWNER_MISMATCH")
    check((member.mode & 0o7777) == (expected["mode"] & 0o7777), "TAR_MODE_MISMATCH")
    mtime = member.pax_headers.get("mtime", str(member.mtime))
    exact = decimal.Decimal(mtime) * decimal.Decimal(1000000000)
    check(exact == decimal.Decimal(expected["mtime_ns"]), "TAR_MTIME_MISMATCH")


def consume(fileobj, capture=False, diff_hash=False):
    digest = hashlib.sha256()
    raw = bytearray() if capture else None
    size = 0
    decompressor = None
    uncompressed = hashlib.sha256()
    first = True
    while True:
        chunk = fileobj.read(CHUNK)
        if not chunk:
            break
        digest.update(chunk)
        size += len(chunk)
        if raw is not None:
            if len(raw) + len(chunk) <= 4 * CHUNK:
                raw.extend(chunk)
            else:
                raw = None
        if diff_hash:
            if first and chunk.startswith(b"\x1f\x8b"):
                decompressor = zlib.decompressobj(16 + zlib.MAX_WBITS)
            if decompressor is not None:
                pending = chunk
                while pending:
                    uncompressed.update(decompressor.decompress(pending, CHUNK))
                    pending = decompressor.unconsumed_tail
                check(not decompressor.unused_data, "DOCKER_LAYER_GZIP_TRAILING_DATA")
            else:
                uncompressed.update(chunk)
        first = False
    if decompressor is not None:
        check(decompressor.eof, "DOCKER_LAYER_GZIP_TRUNCATED")
        uncompressed.update(decompressor.flush())
    result = {"size": size, "sha256": digest.hexdigest()}
    if diff_hash:
        result["diff_sha256"] = uncompressed.hexdigest()
    if raw is not None:
        try:
            result["json"] = json.loads(raw)
        except (ValueError, UnicodeError):
            pass
    return result


def finish_tar(tar):
    # tarfile accepts missing end blocks. Require the first consumed zero header,
    # another zero block and zero-only record padding, then drain to actual EOF.
    check(tar.fileobj.tell() == tar.offset + 512, "TAR_END_BLOCK_MISSING")
    padding = 0
    while True:
        chunk = tar.fileobj.read(CHUNK)
        if not chunk:
            break
        check(not any(chunk), "TAR_TRAILING_DATA")
        padding += len(chunk)
    check(padding >= 512 and padding % 512 == 0, "TAR_END_PADDING_INVALID")


def verify_files(expected):
    check(expected.get("schema") == 1 and isinstance(expected.get("entries"), list), "SOURCE_MANIFEST_INVALID")
    entries = expected["entries"]
    tree = hashlib.sha256(canonical(entries)).hexdigest()
    check(tree == expected.get("tree_sha256"), "SOURCE_TREE_HASH_MISMATCH")
    wanted = {}
    for entry in entries:
        pathname = name(entry["path"], entry["type"] == "dir")
        check(pathname == entry["path"] and pathname not in wanted and entry["type"] in ("file", "dir", "symlink"), "SOURCE_ENTRY_INVALID")
        check(isinstance(entry["mode"], int) and isinstance(entry["uid"], int) and isinstance(entry["gid"], int)
              and isinstance(entry["mtime_ns"], str) and re.fullmatch(r"-?\d+", entry["mtime_ns"]), "SOURCE_METADATA_INVALID")
        if entry["type"] == "file":
            check(isinstance(entry["size"], int) and entry["size"] >= 0 and HEX.fullmatch(entry["sha256"]), "SOURCE_FILE_INVALID")
        if entry["type"] == "symlink":
            check(isinstance(entry.get("link"), str), "SOURCE_LINK_INVALID")
        wanted[pathname] = entry
    seen = {}
    hardlinks = 0
    regular = 0
    logical = 0
    with tarfile.open(fileobj=sys.stdin.buffer, mode="r|", bufsize=512) as tar:
        for member in tar:
            pathname = name(member.name, member.isdir())
            check(pathname in wanted and pathname not in seen, "TAR_ENTRY_UNEXPECTED_OR_DUPLICATE")
            entry = wanted[pathname]
            metadata_matches(member, entry)
            if member.islnk():
                target = name(member.linkname)
                check(target in seen and seen[target].get("sha256"), "TAR_HARDLINK_TARGET_NOT_PREVIOUS_FILE")
                actual = {"size": seen[target]["size"], "sha256": seen[target]["sha256"]}
                hardlinks += 1
            elif member.isreg():
                check(member.size == entry["size"], "TAR_FILE_SIZE_MISMATCH")
                actual = consume(tar.extractfile(member))
                regular += 1
            elif member.issym():
                check(member.linkname == entry["link"], "TAR_SYMLINK_MISMATCH")
                actual = {}
            else:
                actual = {}
            if entry["type"] == "file":
                check(actual["size"] == entry["size"] and actual["sha256"] == entry["sha256"], "TAR_FILE_CONTENT_MISMATCH")
                logical += actual["size"]
            seen[pathname] = actual
        finish_tar(tar)
    check(len(seen) == len(wanted), "TAR_ENTRIES_MISSING")
    return {"status": "PASS", "kind": "files", "entry_count": len(seen), "regular_files": regular,
            "hardlinks": hardlinks, "logical_bytes": logical, "tree_sha256": tree}


def verify_docker(expected):
    ids = expected.get("image_ids")
    check(expected.get("schema") == 1 and isinstance(ids, list) and ids and len(set(ids)) == len(ids)
          and all(isinstance(value, str) and re.fullmatch(r"sha256:[a-f0-9]{64}", value) for value in ids), "DOCKER_EXPECTED_IDS_INVALID")
    records = {}
    links = {}
    json_bytes = 0
    blob_count = 0
    with tarfile.open(fileobj=sys.stdin.buffer, mode="r|", bufsize=512) as tar:
        for member in tar:
            pathname = name(member.name, member.isdir())
            check(pathname not in records and pathname not in links, "DOCKER_TAR_DUPLICATE")
            member_type(member)
            if member.islnk():
                target = name(member.linkname)
                check(target in records and records[target].get("sha256"), "DOCKER_HARDLINK_TARGET_INVALID")
                records[pathname] = records[target]
            elif member.issym():
                target = posixpath.normpath(posixpath.join(posixpath.dirname(pathname), member.linkname))
                links[pathname] = name(target)
            elif member.isdir():
                records[pathname] = {}
            else:
                capture = member.size <= 4 * CHUNK
                record = consume(tar.extractfile(member), capture=capture, diff_hash=True)
                if "json" in record:
                    json_bytes += record["size"]
                    check(json_bytes <= 64 * CHUNK, "DOCKER_JSON_LIMIT")
                records[pathname] = record
            blob = BLOB.fullmatch(pathname)
            if blob:
                check(pathname in records and records[pathname].get("sha256") == blob.group(1), "DOCKER_BLOB_HASH_MISMATCH")
                blob_count += 1
        finish_tar(tar)

    def resolve(pathname, visiting=None):
        pathname = name(pathname)
        if pathname in records:
            check(records[pathname].get("sha256"), "DOCKER_REFERENCE_NOT_FILE")
            return records[pathname]
        visiting = set() if visiting is None else visiting
        check(pathname in links and pathname not in visiting, "DOCKER_REFERENCE_MISSING_OR_CYCLIC")
        return resolve(links[pathname], visiting | {pathname})

    def descriptor(value):
        check(isinstance(value, dict) and re.fullmatch(r"sha256:[a-f0-9]{64}", value.get("digest", "")), "OCI_DESCRIPTOR_INVALID")
        record = resolve("blobs/sha256/" + value["digest"][7:])
        check(record["sha256"] == value["digest"][7:] and record["size"] == value.get("size"), "OCI_DESCRIPTOR_MISMATCH")
        return record

    legacy_configs = set()
    oci_configs = set()
    oci_roots = set()
    repo_tags = set()
    image_count = 0
    manifest_record = records.get("manifest.json", {})
    manifest = manifest_record.get("json")
    check("manifest.json" not in records or "json" in manifest_record, "DOCKER_MANIFEST_NOT_JSON")
    if manifest is not None:
        check(isinstance(manifest, list) and manifest, "DOCKER_MANIFEST_INVALID")
        for image in manifest:
            check(isinstance(image, dict) and isinstance(image.get("Layers"), list), "DOCKER_IMAGE_INVALID")
            config_name = name(image["Config"])
            config = resolve(config_name)
            check("json" in config, "DOCKER_CONFIG_NOT_JSON")
            if re.fullmatch(r"[a-f0-9]{64}\.json", posixpath.basename(config_name)):
                check(posixpath.basename(config_name)[:-5] == config["sha256"], "DOCKER_CONFIG_FILENAME_MISMATCH")
            image_id = "sha256:" + config["sha256"]
            tags = image.get("RepoTags") or []
            check(isinstance(tags, list) and all(isinstance(tag, str) and tag for tag in tags), "DOCKER_REPO_TAGS_INVALID")
            for tag in tags:
                repo_tags.add(tag)
            diffs = config["json"].get("rootfs", {}).get("diff_ids")
            check(isinstance(diffs, list) and len(diffs) == len(image["Layers"]), "DOCKER_LAYER_COUNT_MISMATCH")
            for index, layer_name in enumerate(image["Layers"]):
                layer = resolve(layer_name)
                check("sha256:" + layer["diff_sha256"] == diffs[index], "DOCKER_LAYER_DIFF_HASH_MISMATCH")
            legacy_configs.add(image_id)
            image_count += 1
    # OCI indexes may accompany Docker manifest.json; validate every reachable
    # descriptor as well, including its declared size and content digest.
    visited = {}
    manifest_types = {"application/vnd.oci.image.manifest.v1+json", "application/vnd.docker.distribution.manifest.v2+json"}
    index_types = {"application/vnd.oci.image.index.v1+json", "application/vnd.docker.distribution.manifest.list.v2+json"}

    def walk_descriptor(item, stack):
        record = descriptor(item)
        digest = item["digest"]
        check(digest not in stack, "OCI_DESCRIPTOR_CYCLE")
        check("json" in record, "OCI_MANIFEST_NOT_JSON")
        value = record["json"]
        check(isinstance(value, dict), "OCI_MANIFEST_INVALID")
        types = index_types if "manifests" in value else manifest_types
        check(("mediaType" not in item or item["mediaType"] in types)
              and ("mediaType" not in value or value["mediaType"] in types)
              and ("mediaType" not in item or "mediaType" not in value or item["mediaType"] == value["mediaType"]), "OCI_DESCRIPTOR_MEDIA_TYPE_MISMATCH")
        if digest not in visited:
            visited[digest] = walk_index(value, stack | {digest})
        return visited[digest]

    def walk_index(value, stack):
        check(isinstance(value, dict) and value.get("schemaVersion") == 2, "OCI_MANIFEST_INVALID")
        if "manifests" in value:
            check(isinstance(value["manifests"], list) and value["manifests"], "OCI_INDEX_EMPTY")
            configs = set()
            for item in value["manifests"]:
                configs.update(walk_descriptor(item, stack))
            return configs
        else:
            config = descriptor(value.get("config"))
            check("json" in config and isinstance(value.get("layers"), list), "OCI_IMAGE_INVALID")
            diffs = config["json"].get("rootfs", {}).get("diff_ids")
            check(isinstance(diffs, list) and len(diffs) == len(value["layers"]), "OCI_LAYER_COUNT_MISMATCH")
            for index, item in enumerate(value["layers"]):
                layer = descriptor(item)
                check("sha256:" + layer["diff_sha256"] == diffs[index], "OCI_LAYER_DIFF_HASH_MISMATCH")
            return {"sha256:" + config["sha256"]}

    if "index.json" in records:
        check("json" in records["index.json"], "OCI_INDEX_NOT_JSON")
        index = records["index.json"]["json"]
        check(isinstance(index, dict) and index.get("schemaVersion") == 2
              and isinstance(index.get("manifests"), list) and index["manifests"]
              and index.get("mediaType", "application/vnd.oci.image.index.v1+json") in index_types, "OCI_ROOT_INDEX_INVALID")
        for item in index["manifests"]:
            oci_configs.update(walk_descriptor(item, set()))
            oci_roots.add(item["digest"])
    if manifest is not None and "index.json" in records:
        check(legacy_configs == oci_configs, "DOCKER_OCI_CONFIG_SET_MISMATCH")
    if "repo_tags" in expected:
        tags = expected["repo_tags"]
        check(isinstance(tags, list) and all(isinstance(tag, str) and tag for tag in tags)
              and len(set(tags)) == len(tags), "DOCKER_EXPECTED_REPO_TAGS_INVALID")
        check(manifest is not None and set(tags) == set(repo_tags), "DOCKER_REPO_TAGS_MISMATCH")
    configs = legacy_configs if manifest is not None else oci_configs
    expected_ids = set(ids)
    if configs and expected_ids == configs:
        identity_kind = "config_digest"
    elif oci_roots and expected_ids == oci_roots:
        identity_kind = "oci_root_digest"
    else:
        raise ValueError("DOCKER_IMAGE_IDS_MISMATCH")
    return {"status": "PASS", "kind": "docker", "entry_count": len(records) + len(links),
            "image_count": len(expected_ids), "manifest_entries": image_count, "blob_count": blob_count,
            "identity_kind": identity_kind, "image_ids": sorted(expected_ids),
            "legacy_config_ids": sorted(legacy_configs), "oci_config_ids": sorted(oci_configs), "oci_root_ids": sorted(oci_roots),
            "repo_tags": sorted(repo_tags)}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--kind", choices=("files", "docker"), required=True)
    parser.add_argument("--expected", required=True)
    parser.add_argument("--expected-sha256")
    args = parser.parse_args()
    with open(args.expected, "rb") as fileobj:
        raw = fileobj.read()
    if args.expected_sha256 is not None:
        check(HEX.fullmatch(args.expected_sha256) and hashlib.sha256(raw).hexdigest() == args.expected_sha256, "EXPECTED_MANIFEST_CHANGED")
    expected = json.loads(raw)
    result = verify_files(expected) if args.kind == "files" else verify_docker(expected)
    print(json.dumps(result, sort_keys=True, separators=(",", ":")))


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print("ARCHIVE_TAR_VALIDATION_FAILED:" + str(error), file=sys.stderr)
        sys.exit(1)
