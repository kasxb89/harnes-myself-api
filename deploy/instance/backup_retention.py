#!/usr/bin/env python3
"""Fail-closed retention for the dedicated harness API instance.

Default invocation only plans. A release registry supplies the audited parent
chain; filesystem mtimes never determine which releases survive. Archive I/O
and off-host readback belong to the operator. apply accepts only a signed,
expiring receipt bound to the exact plan and verified archive. No command stops
a container, removes a volume, prunes Docker, or restores a database.

For backup-then-build releases, check before-backup with a positive budget and
--pending-revision/--pending-backup/--pending-output issues a four-hour ticket.
The new retention manifest must include pending_release with ticket_sha256,
pending_revision, parent_revision, and the ticket's exact live object. Pass
--pending-ticket to subsequent checks to permit that one complete fourth set.
After cutover, register the live release; archive and apply before the next one.
Candidate operators must bind retention_ticket_sha256 and base_sha in their
release.json before creation; a staged binary may appear after before-build.

Each release also registers its exact staged binary and frozen operator files.
inspect-artifacts exports these fields. Existing three-set registries migrate
with migrate-artifacts; old operators lacking operator_sha256 require explicit
revision-to-owned-filename lists via --legacy-owned-files. Migration writes
registry metadata only, leaving all archived source trees untouched.

archive-manifest exports exact files entries or Docker image IDs from a plan.
sign-receipt --verification takes a schema-2 envelope with plan_sha256 and
archives:[{metadata,expected,aes_key,verified_receipt}], all real private paths.
Signing requires Node.js >=25 plus the pinned sibling backup_archive.mjs and
verify_backup_archive.py. It physically rechecks the current AES key and runs
two authenticated ciphertext reads, codec checks and full TAR verification for
each archive; this deliberate cost cannot be bypassed by a cached JSON flag.
"""

import argparse
import contextlib
import fcntl
import hashlib
import hmac
import json
import os
from pathlib import Path
import re
import secrets
import shutil
import socket
import stat
import subprocess
import sys
import time


ROOT = Path("/opt/yunshu-harness-newapi-20261016")
OPERATOR_ROOT = Path("/tmp")
NAME = "yunshu-harness-newapi"
IMAGE_REPOSITORY = "yunshu/harness-new-api"
ARCHIVE_CLIENT = Path(__file__).with_name("backup_archive.mjs")
TAR_VALIDATOR = Path(__file__).with_name("verify_backup_archive.py")
ARCHIVE_CLIENT_SHA256 = "4d4084cd93c34b545f3d4d5e2166eccf9a3324dfe120e7332c14ca4953c48dda"
TAR_VALIDATOR_SHA256 = "68ff46870cadd2becd422cc8ee0b8c67984e9e8b9bf73da34448c1dd7065bc46"
REVISION = re.compile(r"[0-9a-f]{40}")
RELEASE = re.compile(r"[0-9]{8}-[0-9a-f]{12}-[0-9]{6}")
DIGEST = re.compile(r"[0-9a-f]{64}")
IMAGE_ID = re.compile(r"sha256:[0-9a-f]{64}")
COMPONENTS = {"database", "configuration", "creative_content", "volumes", "binary", "image"}
MIN_AVAILABLE = 3 * 1024 ** 3
MAX_USED_PERCENT = 93


class Refusal(RuntimeError):
    pass


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=True).encode()


def digest(value):
    return hashlib.sha256(canonical(value)).hexdigest()


def hash_file(path):
    h = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()


def load_json(path):
    return json.loads(Path(path).read_text())


def emit(value):
    print(json.dumps(value, indent=2, sort_keys=True))


def write_registry(path, value):
    temporary = path.with_name(path.name + ".new")
    fd = os.open(str(temporary), os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "w") as stream:
        json.dump(value, stream, indent=2, sort_keys=True)
        stream.write("\n")
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(str(temporary), str(path))


def append_audit(root, plan, event, target=None):
    value = {"time": int(time.time()), "plan_sha256": digest(plan), "event": event}
    if target is not None:
        value["target"] = target
    fd = os.open(str(root / ".backup-retention-audit.jsonl"),
                 os.O_WRONLY | os.O_APPEND | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, "ab") as stream:
        stream.write(canonical(value) + b"\n")
        stream.flush()
        os.fsync(stream.fileno())


def require(condition, message):
    if not condition:
        raise Refusal(message)


def private_key(path):
    path = Path(path)
    s = path.lstat()
    require(stat.S_ISREG(s.st_mode) and not s.st_mode & 0o077,
            "Receipt key must be a regular private file (0600 or stricter)")
    require(s.st_uid == os.geteuid(), "Receipt key must belong to this operator")
    value = path.read_bytes()
    require(len(value) >= 32, "Receipt key needs at least 32 random bytes")
    return value


def check_capacity(path, growth_bytes=0, disk_usage=None):
    require(isinstance(growth_bytes, int) and not isinstance(growth_bytes, bool)
            and growth_bytes >= 0, "Growth budget must be a nonnegative integer")
    usage = disk_usage or os.statvfs(path)
    available = usage.f_bavail * usage.f_frsize
    used = (usage.f_blocks - usage.f_bfree) * usage.f_frsize
    # Match df's treatment of reserved blocks; non-root available is the reserve.
    denominator = used + available
    require(denominator > 0, "Filesystem capacity is unavailable")
    projected_available = available - growth_bytes
    projected_used = 100 * (used + growth_bytes) / denominator
    result = {"available_bytes": available, "growth_bytes": growth_bytes,
              "projected_available_bytes": projected_available,
              "projected_used_percent": projected_used,
              "minimum_available_bytes": MIN_AVAILABLE,
              "maximum_used_percent_exclusive": MAX_USED_PERCENT,
              "pass": projected_available >= MIN_AVAILABLE and projected_used < MAX_USED_PERCENT}
    return result


class Docker:
    def command(self, *args):
        return subprocess.check_output(["docker", *args], text=True).strip()

    def snapshot(self):
        ids = self.command("ps", "--all", "--quiet", "--no-trunc").split()
        containers = json.loads(self.command("inspect", *ids)) if ids else []
        ids = sorted(set(self.command("image", "ls", "--quiet", "--no-trunc").split()))
        images = json.loads(self.command("image", "inspect", *ids)) if ids else []
        return {"containers": containers, "images": images}

    def remove_container(self, container_id):
        # No --force and no --volumes: a concurrently started container is refused.
        self.command("container", "rm", container_id)

    def remove_image_tag(self, tag):
        # An explicit tag, never an image ID, --force, or a prune operation.
        self.command("image", "rm", tag)


def live_identity(snapshot):
    live = [c for c in snapshot["containers"] if c.get("Name") == "/" + NAME]
    require(len(live) == 1, "Exactly one named live API container is required")
    c = live[0]
    revision = (c.get("Config", {}).get("Labels") or {}).get("org.opencontainers.image.revision", "")
    require(c.get("State", {}).get("Status") == "running" and REVISION.fullmatch(revision),
            "Live API must be running with a full source revision label")
    return {"container_id": c["Id"], "image_id": c["Image"], "revision": revision,
            "started_at": c["State"].get("StartedAt"),
            "mounts_sha256": digest(c.get("Mounts", []))}


def tree_fingerprint(path, backup_root, verified_files=None, prefix="release-", directory=True, source_rows=None):
    """Hash content plus deletion-relevant identity; reject symlinks and mounts.

    nlink/ctime are deliberately excluded: old backup binaries may share safe
    hardlinks. Removing another backup link changes nlink/ctime, not its content.
    """
    require(path.parent == backup_root and path.name.startswith(prefix)
            and RELEASE.fullmatch(path.name[len(prefix):]), "Not an exact managed release source")
    parent_stat = backup_root.stat()
    entries = []
    for item in [path, *sorted(path.rglob("*"))]:
        before = item.lstat()
        require(before.st_dev == parent_stat.st_dev and not item.is_mount(),
                "Backup crosses a filesystem or mount boundary")
        require(stat.S_ISDIR(before.st_mode) or stat.S_ISREG(before.st_mode),
                "Backup contains a symlink or special file")
        row = {"path": "." if item == path else item.relative_to(path).as_posix(),
               "device": before.st_dev, "inode": before.st_ino,
               "mode": before.st_mode, "uid": before.st_uid, "gid": before.st_gid,
               "size": before.st_size, "mtime_ns": before.st_mtime_ns}
        if stat.S_ISREG(before.st_mode):
            row["sha256"] = hash_file(item)
            if verified_files is not None:
                verified_files[row["path"]] = {"bytes": before.st_size, "sha256": row["sha256"]}
        after = item.lstat()
        require((before.st_dev, before.st_ino, before.st_mode, before.st_size, before.st_mtime_ns)
                == (after.st_dev, after.st_ino, after.st_mode, after.st_size, after.st_mtime_ns),
                "Backup changed while hashing")
        entries.append(row)
    require(path.is_dir() if directory else path.is_file(), "Release source has the wrong type")
    if source_rows is not None:
        source_rows.extend(entries)
    return {"sha256": digest(entries), "files": sum("sha256" in e for e in entries),
            "logical_bytes": sum(e["size"] for e in entries if "sha256" in e)}


def validate_complete_backup(root, release):
    path = root / "backup" / release["backup"]
    verified_files = {}
    source_rows = []
    fingerprint = tree_fingerprint(path, root / "backup", verified_files, source_rows=source_rows)
    manifest_name = release.get("manifest", "retention-manifest.json")
    require(Path(manifest_name).name == manifest_name, "Manifest must be inside its release backup")
    manifest_path = path / manifest_name
    require(DIGEST.fullmatch(release.get("manifest_sha256", ""))
            and verified_files.get(manifest_name, {}).get("sha256") == release["manifest_sha256"], "Backup manifest changed")
    manifest = load_json(manifest_path)
    require(manifest.get("complete") is True and manifest.get("revision") == release["revision"]
            and set(manifest.get("components", [])) == COMPONENTS,
            "Backup is not a complete, revision-bound restore set")
    files = manifest.get("files", {})
    require(isinstance(files, dict) and files, "Backup manifest has no verified files")
    for relative, expected in files.items():
        file = Path(relative)
        require(not file.is_absolute() and ".." not in file.parts and file.parts,
                "Manifest file escapes backup")
        candidate = path / file
        require(candidate.is_file() and not candidate.is_symlink()
                and isinstance(expected, dict) and candidate.stat().st_size == expected.get("bytes")
                and verified_files.get(file.as_posix()) == expected, "Backup file verification failed")
    return {"path": "backup/" + release["backup"], "fingerprint": fingerprint,
            "manifest_sha256": release["manifest_sha256"], "revision": release["revision"], "source_rows": source_rows}


def protected_mount(path, snapshot):
    for container in snapshot["containers"]:
        for mount in container.get("Mounts", []):
            source = mount.get("Source")
            if source:
                source = Path(source).resolve()
                if source == path or source in path.parents or path in source.parents:
                    return True
    return False


def artifact_paths(root, release):
    require(RELEASE.fullmatch(release.get("release", "")), "Invalid artifact release")
    return {"staged_binary": root / ("new-api-" + release["release"]),
            "operator_dir": OPERATOR_ROOT / ("yunshu-api-" + release["release"])}


def inspect_release_artifacts(root, release, recorded=None, legacy_owned_files=None):
    paths = artifact_paths(root, release)
    require(OPERATOR_ROOT.resolve() == OPERATOR_ROOT and OPERATOR_ROOT.is_dir(), "Invalid operator scope root")
    binary_files = {}
    binary_rows = []
    binary_fingerprint = tree_fingerprint(paths["staged_binary"], root, binary_files, "new-api-", False, binary_rows)
    operator_files = {}
    operator_rows = []
    operator_fingerprint = tree_fingerprint(paths["operator_dir"], OPERATOR_ROOT, operator_files, "yunshu-api-", True, operator_rows)
    info = load_json(paths["operator_dir"] / "release.json")
    binary_sha = binary_files["."]["sha256"]
    require(info.get("source_sha") == release["revision"] and info.get("release") == release["release"]
            and info.get("binary_sha256") == binary_sha, "Operator source/release/binary binding differs")
    require(all(not info.get(field) or info[field] == str(paths[kind]) for field, kind in
                (("staged_binary", "staged_binary"), ("operator_dir", "operator_dir")))
            and info.get("container", NAME) == NAME, "Operator artifact paths or container differ")
    hashes = info.get("operator_sha256", {})
    frozen = {"release.json": operator_files["release.json"]}
    if isinstance(hashes, dict) and hashes:
        origin = "release-operator_sha256"
        for name, expected_hash in hashes.items():
            require(Path(name).name == name and DIGEST.fullmatch(expected_hash)
                    and operator_files.get(name, {}).get("sha256") == expected_hash,
                    "Frozen operator source differs")
            frozen[name] = operator_files[name]
    else:
        origin = "explicit-legacy-owned-files"
        if legacy_owned_files is None and recorded is not None:
            require(recorded.get("operator_dir", {}).get("manifest_origin") == origin,
                    "Legacy operators need an explicit migration")
            legacy_owned_files = list(recorded["operator_dir"].get("files", {}))
        require(isinstance(legacy_owned_files, list) and len(set(legacy_owned_files)) == len(legacy_owned_files)
                and {"release.json", "deploy-preserving-instance.py", "rollback-instance.py", "backup-instance.py"}
                <= set(legacy_owned_files), "Explicit historical owned-file manifest required")
        for name in legacy_owned_files:
            require(Path(name).name == name and name in operator_files,
                    "Legacy owned-file manifest must list real flat operator files")
            frozen[name] = operator_files[name]
    for path_key, hash_key in (("plugin_manager", "plugin_manager_sha256"),
                               ("plugin_source", "plugin_source_sha256")):
        if info.get(path_key):
            path = Path(info[path_key])
            require(path.parent == paths["operator_dir"] and DIGEST.fullmatch(info.get(hash_key, ""))
                    and operator_files.get(path.name, {}).get("sha256") == info[hash_key],
                    "Frozen operator dependency differs")
            frozen[path.name] = operator_files[path.name]
    artifacts = {"staged_binary": {"path": str(paths["staged_binary"]), "sha256": binary_sha},
                 "operator_dir": {"path": str(paths["operator_dir"]), "files": frozen,
                                  "manifest_sha256": digest(frozen), "manifest_origin": origin}}
    if recorded is not None:
        require(recorded == artifacts, "Registered release artifacts changed or lack exact source bindings")
    sources = [
        {"kind": "staged_binary", "path": str(paths["staged_binary"]), "revision": release["revision"],
         "fingerprint": binary_fingerprint, "source_sha256": binary_sha, "source_rows": binary_rows},
        {"kind": "operator_dir", "path": str(paths["operator_dir"]), "revision": release["revision"],
         "fingerprint": operator_fingerprint, "source_sha256": digest(frozen), "source_rows": operator_rows}]
    return artifacts, sources


def archive_source_fingerprints(plan):
    return {source["path"]: source["fingerprint"]["sha256"]
            for source in [*plan["remove_backups"], *plan["remove_artifacts"]]}


def files_archive_manifest(plan):
    entries = []
    for source in [*plan["remove_backups"], *plan["remove_artifacts"]]:
        rows = source.get("source_rows", [])
        require(rows and digest(rows) == source["fingerprint"]["sha256"]
                and sum("sha256" in row for row in rows) == source["fingerprint"]["files"]
                and sum(row["size"] for row in rows if "sha256" in row) == source["fingerprint"]["logical_bytes"],
                "Plan lacks the exact source content/metadata rows")
        root = Path(source["path"])
        if not root.is_absolute():
            root = Path(plan["root"]) / root
        for row in rows:
            relative = Path(row["path"])
            require(not relative.is_absolute() and ".." not in relative.parts,
                    "Source content row escapes its planned root")
            path = root if row["path"] == "." else root / relative
            file = stat.S_ISREG(row["mode"])
            require(file or stat.S_ISDIR(row["mode"]), "Unsupported source content row type")
            entry = {"path": path.as_posix().lstrip("/"), "type": "file" if file else "dir",
                     "mode": row["mode"], "uid": row["uid"], "gid": row["gid"], "mtime_ns": str(row["mtime_ns"])}
            if file:
                require(DIGEST.fullmatch(row.get("sha256", "")), "Source file row lacks its content digest")
                entry.update({"size": row["size"], "sha256": row["sha256"]})
            entries.append(entry)
    entries.sort(key=lambda entry: entry["path"])
    require(len({entry["path"] for entry in entries}) == len(entries), "Planned archive source paths overlap")
    tree = hashlib.sha256(json.dumps(entries, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
    return {"schema": 1, "plan_sha256": digest(plan), "source_fingerprints": archive_source_fingerprints(plan),
            "entries": entries, "tree_sha256": tree}


def required_docker_image_ids(plan):
    return sorted({candidate["image_id"] for candidate in [*plan["remove_containers"], *plan["remove_image_tags"]]})


def create_plan(root, registry, snapshot, host_id=None):
    require(root == ROOT and root.resolve() == root and root.is_dir(), "Wrong or symlinked API root")
    require((root / "backup").is_dir() and not (root / "backup").is_symlink(), "Invalid backup root")
    require(registry.get("schema") == 1 and registry.get("root") == str(ROOT)
            and registry.get("container") == NAME and registry.get("rollback_count") == 2,
            "Registry instance or retention policy mismatch")
    live = live_identity(snapshot)
    require(registry.get("head_revision") == live["revision"], "Registry head is not the live source revision")
    releases = registry.get("releases", [])
    by_revision = {}
    for release in releases:
        revision = release.get("revision", "")
        require(REVISION.fullmatch(revision) and revision not in by_revision, "Invalid or duplicate revision")
        require(RELEASE.fullmatch(release.get("release", ""))
                and release.get("backup") == "release-" + release["release"]
                and release["release"].split("-")[1] == revision[:12], "Release name/revision mismatch")
        require(IMAGE_ID.fullmatch(release.get("image_id", "")), "Release image identity is missing")
        by_revision[revision] = release
    keep = []
    revision = live["revision"]
    for _ in range(3):
        require(revision in by_revision and revision not in keep, "Current plus two complete predecessors required")
        keep.append(revision)
        revision = by_revision[revision].get("parent_revision")
    ancestors = set(keep)
    while revision in by_revision:
        require(revision not in ancestors, "Release parent chain contains a cycle")
        ancestors.add(revision)
        revision = by_revision[revision].get("parent_revision")
    require(ancestors == set(by_revision), "Registry contains a release outside the live parent chain")
    require(by_revision[keep[0]]["image_id"] == live["image_id"], "Current registry image is not live")
    verified = {r["revision"]: validate_complete_backup(root, r) for r in releases}
    artifact_sources = {}
    for release in releases:
        require(isinstance(release.get("artifacts"), dict), "Release artifact registration or explicit migration required")
        _, artifact_sources[release["revision"]] = inspect_release_artifacts(root, release, release["artifacts"])
    obsolete = set(by_revision) - set(keep)
    remove_backups = []
    for revision in sorted(obsolete):
        item = verified[revision]
        require(not protected_mount(root / item["path"], snapshot), "A container mounts a cleanup candidate")
        remove_backups.append(item)
    remove_artifacts = []
    for revision in sorted(obsolete):
        for item in artifact_sources[revision]:
            require(not protected_mount(Path(item["path"]), snapshot), "A container mounts a release artifact candidate")
            remove_artifacts.append(item)
    retained_images = {by_revision[r]["image_id"] for r in keep}
    remove_containers = []
    for container in snapshot["containers"]:
        labels = container.get("Config", {}).get("Labels") or {}
        revision = labels.get("org.opencontainers.image.revision")
        name = container.get("Name", "").removeprefix("/")
        suffix = name.removeprefix(NAME + "-rollback-")
        if name.startswith(NAME + "-rollback-") and RELEASE.fullmatch(suffix) and revision in obsolete:
            if container.get("State", {}).get("Status") != "exited":
                continue
            require(container["Image"] == by_revision[revision]["image_id"], "Rollback container image mismatch")
            if container["Image"] not in retained_images:
                remove_containers.append({"id": container["Id"], "name": name,
                                          "image_id": container["Image"], "revision": revision})
    removable_ids = {c["id"] for c in remove_containers}
    referenced_images = {c["Image"] for c in snapshot["containers"] if c["Id"] not in removable_ids}
    remove_tags = []
    for image in snapshot["images"]:
        image_id = image["Id"]
        revision = (image.get("Config", {}).get("Labels") or {}).get("org.opencontainers.image.revision")
        if revision not in obsolete or image_id in retained_images | referenced_images:
            continue
        require(image_id == by_revision[revision]["image_id"], "Obsolete image identity mismatch")
        tags = image.get("RepoTags") or []
        # Shared/foreign tags are protected, even if a harness tag is also present.
        if not tags or any(not tag.startswith(IMAGE_REPOSITORY + ":") for tag in tags):
            continue
        for tag in tags:
            suffix = tag.split(":", 1)[1].removeprefix("rollback-")
            if RELEASE.fullmatch(suffix):
                remove_tags.append({"tag": tag, "image_id": image_id, "revision": revision})
    managed = {r["backup"] for r in releases}
    registered_artifacts = {source["path"] for sources in artifact_sources.values() for source in sources}
    observed_artifacts = []
    # Only these two exact one-level namespaces are inventoried. Unknown /tmp
    # trees are never traversed, fingerprinted, or selected for deletion.
    for parent, prefix in ((root, "new-api-"), (OPERATOR_ROOT, "yunshu-api-")):
        observed_artifacts.extend(str(p) for p in parent.iterdir()
                                  if p.name.startswith(prefix) and RELEASE.fullmatch(p.name[len(prefix):]))
    return {"schema": 1, "purpose": "harness-api-retention", "root": str(root), "container": NAME,
            "host_id": host_id or socket.gethostname(), "created_at": int(time.time()), "live": live,
            "registry_sha256": digest(registry), "keep_revisions": keep,
            "keep_backups": [verified[r] for r in keep], "remove_backups": remove_backups,
            "keep_artifacts": [by_revision[r]["artifacts"] for r in keep],
            "remove_artifacts": remove_artifacts,
            "unregistered_artifacts": sorted(set(observed_artifacts) - registered_artifacts),
            "remove_containers": sorted(remove_containers, key=lambda c: c["id"]),
            "remove_image_tags": sorted(remove_tags, key=lambda i: i["tag"]),
            "protected_unmanaged": sorted(p.name for p in (root / "backup").iterdir() if p.name not in managed)}


def validate_pending_backup(root, registry, snapshot, pending, unmanaged_releases, unmanaged_artifacts, now=None,
                            complete_artifacts=False):
    now = int(time.time()) if now is None else now
    require(pending.get("schema") == 1 and pending.get("purpose") == "harness-api-pending-release"
            and pending.get("root") == str(root) and pending.get("container") == NAME
            and pending.get("registry_sha256") == digest(registry)
            and pending.get("parent_revision") == registry["head_revision"]
            and pending.get("live") == live_identity(snapshot), "Pending release baseline changed")
    require(isinstance(pending.get("created_at"), int) and isinstance(pending.get("expires_at"), int)
            and pending["created_at"] <= now <= pending["expires_at"]
            and pending["expires_at"] - pending["created_at"] == 14400
            and re.fullmatch("[0-9a-f]{32}", pending.get("nonce", "")),
            "Pending release ticket expired or has an invalid lifetime")
    revision = pending.get("pending_revision", "")
    backup = pending.get("pending_backup", "")
    require(REVISION.fullmatch(revision) and revision not in {r["revision"] for r in registry["releases"]}
            and backup.startswith("release-") and RELEASE.fullmatch(backup[8:])
            and backup[8:].split("-")[1] == revision[:12]
            and unmanaged_releases == [backup], "Only the exact single pending backup may exceed three")
    manifest = load_json(root / "backup" / backup / "retention-manifest.json")
    require(manifest.get("pending_release") == {
            "ticket_sha256": digest(pending), "pending_revision": revision,
            "parent_revision": registry["head_revision"], "live": pending["live"]},
            "Pending backup manifest does not bind the ticket and actual live source")
    validate_complete_backup(root, {"backup": backup, "revision": revision,
                                   "manifest": "retention-manifest.json",
                                   "manifest_sha256": hash_file(root / "backup" / backup / "retention-manifest.json")})
    paths = artifact_paths(root, {"release": backup[8:]})
    require(set(unmanaged_artifacts) <= {str(p) for p in paths.values()},
            "An artifact outside the exact pending release remains unregistered")
    operator_exists = paths["operator_dir"].exists() or paths["operator_dir"].is_symlink()
    binary_exists = paths["staged_binary"].exists() or paths["staged_binary"].is_symlink()
    if complete_artifacts:
        require(operator_exists and binary_exists, "Before cutover requires both complete candidate artifacts")
    if operator_exists:
        require(not protected_mount(paths["operator_dir"], snapshot), "Pending operators have a container mount reference")
        if not complete_artifacts:
            tree_fingerprint(paths["operator_dir"], OPERATOR_ROOT, prefix="yunshu-api-")
        info = load_json(paths["operator_dir"] / "release.json")
        require(info.get("source_sha") == revision and info.get("release") == backup[8:]
                and info.get("base_sha") == pending["parent_revision"]
                and info.get("retention_ticket_sha256") == digest(pending),
                "Pending operators do not bind this release ticket and live parent")
    else:
        info = {}
    if binary_exists:
        require(not protected_mount(paths["staged_binary"], snapshot), "Pending binary has a container mount reference")
        if not complete_artifacts:
            files = {}
            tree_fingerprint(paths["staged_binary"], root, files, "new-api-", False)
            require(files["."]["sha256"] == info.get("binary_sha256"),
                    "Pending binary lacks the exact candidate operator hash binding")
    if complete_artifacts:
        artifacts, _ = inspect_release_artifacts(root, {"release": backup[8:], "revision": revision})
        require(artifacts["operator_dir"]["manifest_origin"] == "release-operator_sha256",
                "Before cutover requires the complete native frozen operator manifest")


def check_release_gate(root, registry, snapshot, phase, growth_bytes=None, disk_usage=None, pending=None, now=None):
    require(phase in {"before-backup", "before-build", "after-backup", "before-cutover"}, "Invalid release phase")
    before_growth = phase in {"before-backup", "before-build"}
    if before_growth:
        require(isinstance(growth_bytes, int) and not isinstance(growth_bytes, bool) and growth_bytes > 0,
                "Before backup/build requires an explicit positive growth budget")
    elif growth_bytes is None:
        growth_bytes = 0
    plan = create_plan(root, registry, snapshot)
    capacity = check_capacity(root, growth_bytes, disk_usage)
    unmanaged_releases = [name for name in plan["protected_unmanaged"]
                          if name.startswith("release-") and RELEASE.fullmatch(name[8:])]
    # The backup for this exact release may precede its image build. Its ticket
    # was issued against a clean three-set baseline before the backup existed.
    count_ok = not plan["remove_backups"] and not unmanaged_releases and not plan["unregistered_artifacts"]
    if pending is not None and phase != "before-backup":
        require(not plan["remove_backups"], "An older unarchived release cannot use a pending exception")
        validate_pending_backup(root, registry, snapshot, pending, unmanaged_releases, plan["unregistered_artifacts"], now,
                                complete_artifacts=phase == "before-cutover")
        count_ok = True
    require_clean = before_growth or phase == "before-cutover"
    return {"status": "PASS" if capacity["pass"] and (count_ok or not require_clean) else "BLOCKED",
            "phase": phase, "capacity": capacity, "retained_backup_count": 3,
            "unarchived_obsolete_backups": [b["path"] for b in plan["remove_backups"]],
            "unregistered_release_backups": unmanaged_releases,
            "unregistered_artifacts": plan["unregistered_artifacts"],
            "quantity_gate_pass": count_ok, "generation_requests": 0}


def prepare_pending_release(root, registry, snapshot, revision, backup, growth_bytes, disk_usage=None, now=None):
    result = check_release_gate(root, registry, snapshot, "before-backup", growth_bytes, disk_usage)
    require(result["status"] == "PASS", "A pending release requires a clean, capacity-safe baseline")
    require(REVISION.fullmatch(revision) and revision not in {r["revision"] for r in registry["releases"]}
            and backup.startswith("release-") and RELEASE.fullmatch(backup[8:])
            and backup[8:].split("-")[1] == revision[:12]
            and not (root / "backup" / backup).exists(), "Pending backup must be new and revision-bound")
    now = int(time.time()) if now is None else now
    result["pending_ticket"] = {"schema": 1, "purpose": "harness-api-pending-release",
                                "root": str(root), "container": NAME, "pending_revision": revision,
                                "pending_backup": backup, "parent_revision": registry["head_revision"],
                                "registry_sha256": digest(registry), "live": live_identity(snapshot),
                                "created_at": now, "expires_at": now + 14400, "nonce": secrets.token_hex(16)}
    return result


def register_live_release(root, registry, record, snapshot):
    require(registry.get("schema") == 1 and registry.get("head_revision")
            and len(registry.get("releases", [])) == 3, "Register requires a cleaned current-plus-two registry")
    require(record.get("parent_revision") == registry["head_revision"], "New release parent differs from old head")
    require(record.get("revision") == live_identity(snapshot)["revision"], "Only an already-live release can register")
    require(record["revision"] not in {r["revision"] for r in registry["releases"]}, "Release is already registered")
    require(record.get("artifacts", {}).get("operator_dir", {}).get("manifest_origin") == "release-operator_sha256",
            "New releases require the native frozen operator source manifest")
    updated = {**registry, "head_revision": record["revision"], "releases": [record, *registry["releases"]]}
    plan = create_plan(root, updated, snapshot)
    require(len(plan["remove_backups"]) == 1 and not plan["unregistered_artifacts"],
            "Registration must retire exactly one predecessor without stray artifacts")
    return updated


def migrate_release_artifacts(root, registry, snapshot, legacy_owned_files=None):
    require(len(registry.get("releases", [])) == 3, "Artifact migration is only for the three retained releases")
    legacy_owned_files = {} if legacy_owned_files is None else legacy_owned_files
    require(isinstance(legacy_owned_files, dict) and set(legacy_owned_files)
            <= {r["revision"] for r in registry["releases"]}, "Legacy file lists must name retained revisions")
    releases = []
    for record in registry["releases"]:
        artifacts, _ = inspect_release_artifacts(root, record, record.get("artifacts"),
                                                 legacy_owned_files.get(record["revision"]))
        releases.append({**record, "artifacts": artifacts})
    updated = {**registry, "releases": releases}
    plan = create_plan(root, updated, snapshot)
    require(not plan["remove_backups"] and not plan["unregistered_artifacts"],
            "Migration requires the current three sets and no unregistered staged/operator artifacts")
    return updated


def private_archive_stat(filename):
    path = Path(filename)
    require(path.is_absolute() and path.resolve() == path, "Archive evidence needs a real absolute private path")
    value = path.lstat()
    parent = path.parent.lstat()
    require(stat.S_ISREG(value.st_mode) and value.st_uid == os.geteuid()
            and stat.S_IMODE(value.st_mode) == 0o600 and stat.S_ISDIR(parent.st_mode)
            and parent.st_uid == os.geteuid() and stat.S_IMODE(parent.st_mode) == 0o700,
            "Archive evidence requires an owned 0600 file inside an owned 0700 directory")
    return (value.st_dev, value.st_ino, value.st_mode, value.st_uid, value.st_gid,
            value.st_size, value.st_mtime_ns, value.st_ctime_ns)


def read_private_evidence(filename):
    before = private_archive_stat(filename)
    require(before[5] <= 16 * 1024 ** 2, "Archive evidence JSON/key exceeds its size limit")
    fd = os.open(str(filename), os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(fd, "rb") as stream:
        data = stream.read()
    require(private_archive_stat(filename) == before, "Archive evidence changed while reading")
    return data


def validate_crypto_verification(verification):
    codec = verification.get("compression")
    require(verification.get("schema") == 1 and verification.get("status") == "VERIFIED"
            and verification.get("authenticatedReadPasses") == 2 and verification.get("gcmAuthenticated") is True
            and verification.get("noExtraction") is True
            and ((codec == "gzip" and verification.get("gzipCrcVerified") is True)
                 or (codec == "zstd" and verification.get("zstdChecksumVerified") is True))
            and verification.get("tar", {}).get("status") == "PASS",
            "Actual two-pass AES-GCM, codec and TAR verification required")


def run_archive_verifier(metadata, expected, aes_key):
    require(hash_file(ARCHIVE_CLIENT) == ARCHIVE_CLIENT_SHA256 and hash_file(TAR_VALIDATOR) == TAR_VALIDATOR_SHA256,
            "Reviewed archive verifier source changed")
    node = shutil.which("node")
    require(node is not None, "Node.js 25 or newer is required for archive signing")
    version = subprocess.check_output([node, "--version"], text=True).strip()
    require(re.fullmatch(r"v(\d+)\.\d+\.\d+", version) and int(version.split(".")[0][1:]) >= 25,
            "Node.js 25 or newer is required for archive signing")
    result = subprocess.run([node, str(ARCHIVE_CLIENT), "verify", "--metadata", str(metadata),
                             "--expected", str(expected), "--key-file", str(aes_key)],
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, timeout=14400)
    require(result.returncode == 0 and len(result.stdout) <= 2 * 1024 ** 2, "Archive client verification failed")
    require(hash_file(ARCHIVE_CLIENT) == ARCHIVE_CLIENT_SHA256 and hash_file(TAR_VALIDATOR) == TAR_VALIDATOR_SHA256,
            "Archive verifier changed during signing")
    return json.loads(result.stdout)


def verify_crypto_archive(plan, item):
    require(set(item) == {"metadata", "expected", "aes_key", "verified_receipt"}, "Exact physical archive evidence paths required")
    metadata_path, expected_path, aes_key_path, proof_path = [Path(item[k]) for k in
                                                            ("metadata", "expected", "aes_key", "verified_receipt")]
    metadata_bytes = read_private_evidence(metadata_path)
    expected_bytes = read_private_evidence(expected_path)
    proof_bytes = read_private_evidence(proof_path)
    aes_key = read_private_evidence(aes_key_path)
    require(len(aes_key) == 32 and aes_key_path.parent != metadata_path.parent
            and aes_key_path.parent not in metadata_path.parent.parents
            and metadata_path.parent not in aes_key_path.parent.parents, "AES key must be 32 bytes and stored separately")
    metadata, expected, prior = [json.loads(value) for value in (metadata_bytes, expected_bytes, proof_bytes)]
    key_hash = hashlib.sha256(aes_key).hexdigest()
    metadata_hash = hashlib.sha256(metadata_bytes).hexdigest()
    expected_hash = hashlib.sha256(expected_bytes).hexdigest()
    validate_crypto_verification(prior)
    require(metadata.get("algorithm") == "aes-256-gcm" and metadata.get("header", {}).get("algorithm") == "aes-256-gcm"
            and prior.get("metadataSha256") == metadata_hash and prior.get("aesKeySha256") == key_hash
            and prior.get("expectedManifestSha256") == expected_hash
            and metadata["header"].get("expectedManifestSha256") == expected_hash
            and prior.get("archiveId") == metadata.get("archiveId")
            and prior.get("ciphertext") == metadata.get("ciphertext"),
            "Physical metadata, expected manifest, current AES key and VERIFIED receipt differ")
    kind = metadata["header"].get("kind")
    if kind == "files":
        require(expected == files_archive_manifest(plan), "TAR expected entries do not exactly cover the planned source rows")
    else:
        ids = expected.get("image_ids", [])
        require(kind == "docker" and set(expected) == {"schema", "image_ids"} and expected["schema"] == 1
                and isinstance(ids, list) and ids and ids == sorted(set(ids))
                and set(ids) <= set(required_docker_image_ids(plan)), "Docker archive must bind exact planned image identities")
    filename = metadata.get("ciphertextFile", "")
    require(filename and Path(filename).name == filename, "Invalid ciphertext filename")
    ciphertext = metadata_path.parent / filename
    before = {str(p): private_archive_stat(p) for p in (metadata_path, expected_path, aes_key_path, proof_path, ciphertext)}
    require(before[str(ciphertext)][5] == metadata["ciphertext"]["bytes"] and before[str(ciphertext)][5] > 0,
            "Physical ciphertext size differs")
    # This intentionally rereads ciphertext twice: the pinned client authenticates
    # pass one before decompression and checks codec + every TAR entry in pass two.
    fresh = run_archive_verifier(metadata_path, expected_path, aes_key_path)
    validate_crypto_verification(fresh)
    ignored = {"verifiedAt", "verificationReceipt"}
    require({k: v for k, v in fresh.items() if k not in ignored}
            == {k: v for k, v in prior.items() if k not in ignored}, "Current physical verification differs from its prior receipt")
    require(all(private_archive_stat(Path(p)) == value for p, value in before.items()), "Archive/key evidence changed during signing")
    physical_fresh = read_private_evidence(fresh["verificationReceipt"])
    verification = {k: v for k, v in fresh.items() if k != "verificationReceipt"}
    require(json.loads(physical_fresh) == verification, "Fresh physical verification receipt differs")
    return {"kind": kind, "locator": str(ciphertext), "sha256": metadata["ciphertext"]["sha256"],
            "bytes": metadata["ciphertext"]["bytes"], "metadata_locator": str(metadata_path),
            "metadata_sha256": metadata_hash, "expected_locator": str(expected_path),
            "expected_manifest_sha256": expected_hash, "aes_key_locator": str(aes_key_path),
            "aes_key_sha256": key_hash, "verification_sha256": hashlib.sha256(physical_fresh).hexdigest(),
            "verification": verification, "files_tree_sha256": expected.get("tree_sha256"),
            "image_ids": expected.get("image_ids", []),
            "privacy": {"owner_uid": os.geteuid(), "file_mode": "0600", "parent_mode": "0700", "separate_aes_key": True}}


def sign_archive_receipt(plan, envelope, key, now=None):
    now = int(time.time()) if now is None else now
    require(socket.gethostname() != plan["host_id"], "Signing must run on the off-host archive machine")
    require(envelope.get("schema") == 2 and envelope.get("plan_sha256") == digest(plan)
            and isinstance(envelope.get("archives"), list) and envelope["archives"],
            "Physical AES archive verification envelope required; generic verified flags are not accepted")
    archives = [verify_crypto_archive(plan, item) for item in envelope["archives"]]
    require(all(archive["aes_key_sha256"] != hashlib.sha256(key).hexdigest() for archive in archives),
            "Archive AES keys and the receipt HMAC signing key must differ")
    receipt = {"schema": 2, "purpose": "harness-api-retention-archive", "root": str(ROOT),
               "host_id": plan["host_id"], "offhost_id": socket.gethostname(), "plan_sha256": digest(plan),
               "expires_at": now + 3600, "source_fingerprints": archive_source_fingerprints(plan),
               "required_image_ids": required_docker_image_ids(plan), "archives": archives,
               "verifier": {"client_sha256": ARCHIVE_CLIENT_SHA256, "tar_validator_sha256": TAR_VALIDATOR_SHA256},
               "allowlist": {k: plan[k] for k in ("remove_backups", "remove_artifacts", "remove_containers", "remove_image_tags")}}
    receipt["signature"] = hmac.new(key, canonical(receipt), hashlib.sha256).hexdigest()
    verify_receipt(plan, receipt, key, now)
    return receipt


def verify_receipt(plan, receipt, key, now=None):
    signature = receipt.get("signature", "")
    unsigned = {k: v for k, v in receipt.items() if k != "signature"}
    require(hmac.compare_digest(signature, hmac.new(key, canonical(unsigned), hashlib.sha256).hexdigest()),
            "Receipt signature is invalid")
    now = int(time.time()) if now is None else now
    require(receipt.get("schema") == 2 and receipt.get("purpose") == "harness-api-retention-archive"
            and receipt.get("plan_sha256") == digest(plan)
            and receipt.get("host_id") == plan["host_id"] and receipt.get("root") == str(ROOT),
            "Receipt does not bind this exact plan and instance")
    require(now <= receipt.get("expires_at", 0) <= now + 86400, "Receipt expired or exceeds 24 hours")
    require(receipt.get("allowlist") == {k: plan[k] for k in
            ("remove_backups", "remove_artifacts", "remove_containers", "remove_image_tags")}, "Signed deletion allowlist differs")
    require(receipt.get("offhost_id") and receipt["offhost_id"] != plan["host_id"]
            and receipt.get("source_fingerprints") == archive_source_fingerprints(plan)
            and receipt.get("required_image_ids") == required_docker_image_ids(plan)
            and receipt.get("verifier") == {"client_sha256": ARCHIVE_CLIENT_SHA256, "tar_validator_sha256": TAR_VALIDATOR_SHA256},
            "Signed off-host source and verifier bindings differ")
    file_proofs = 0
    image_ids = []
    for archive in receipt.get("archives", []):
        verification = archive.get("verification", {})
        validate_crypto_verification(verification)
        privacy = archive.get("privacy", {})
        require(Path(archive.get("locator", "")).is_absolute()
                and privacy.get("file_mode") == "0600" and privacy.get("parent_mode") == "0700"
                and privacy.get("separate_aes_key") is True and isinstance(privacy.get("owner_uid"), int)
                and not isinstance(privacy["owner_uid"], bool)
                and all(DIGEST.fullmatch(archive.get(field, "")) for field in
                        ("sha256", "metadata_sha256", "expected_manifest_sha256", "aes_key_sha256"))
                and isinstance(archive.get("bytes"), int) and not isinstance(archive["bytes"], bool) and archive["bytes"] > 0
                and DIGEST.fullmatch(archive.get("verification_sha256", ""))
                and verification.get("metadataSha256") == archive.get("metadata_sha256")
                and verification.get("expectedManifestSha256") == archive.get("expected_manifest_sha256")
                and verification.get("aesKeySha256") == archive.get("aes_key_sha256")
                and verification.get("ciphertext") == {"sha256": archive.get("sha256"), "bytes": archive.get("bytes")},
                "Signed physical AES archive evidence is incomplete")
        if archive.get("kind") == "files":
            manifest = files_archive_manifest(plan)
            require(verification["tar"].get("kind") == "files"
                    and verification["tar"].get("tree_sha256") == manifest["tree_sha256"]
                    and verification["tar"].get("entry_count") == len(manifest["entries"])
                    and archive.get("files_tree_sha256") == manifest["tree_sha256"], "Files archive source coverage differs")
            file_proofs += 1
        else:
            require(archive.get("kind") == "docker" and verification["tar"].get("kind") == "docker"
                    and verification["tar"].get("image_ids") == archive.get("image_ids"), "Docker archive image proof differs")
            image_ids.extend(archive["image_ids"])
    require(file_proofs == 1 and sorted(image_ids) == required_docker_image_ids(plan),
            "A complete files archive and every distinct Docker deletion image are required")


@contextlib.contextmanager
def deployment_lock(root):
    # The same lock used by release operators; O_NOFOLLOW prevents lock redirection.
    fd = os.open(str(root / ".deploy.lock"), os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    try:
        fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        yield
    finally:
        os.close(fd)


def apply_plan(root, registry, plan, receipt, key, docker, registry_path=None):
    verify_receipt(plan, receipt, key)
    require(plan.get("host_id") == socket.gethostname(), "Plan belongs to a different host")
    with deployment_lock(root):
        if registry_path is not None:
            require(digest(load_json(registry_path)) == digest(registry), "Registry changed before cleanup")
        fresh = create_plan(root, registry, docker.snapshot(), plan["host_id"])
        require({k: v for k, v in fresh.items() if k != "created_at"}
                == {k: v for k, v in plan.items() if k != "created_at"}, "Live state or sources changed after planning")
        removed = {"backups": [], "artifacts": [], "containers": [], "image_tags": []}
        append_audit(root, plan, "verified-start")
        for backup in plan["remove_backups"]:
            snapshot = docker.snapshot()
            require(live_identity(snapshot) == plan["live"], "Live API changed during cleanup")
            path = root / backup["path"]
            require(not protected_mount(path, snapshot) and tree_fingerprint(path, root / "backup") == backup["fingerprint"],
                    "Backup changed or became mounted")
            require(shutil.rmtree.avoids_symlink_attacks, "Platform lacks safe fd-based tree deletion")
            append_audit(root, plan, "remove-backup-start", backup["path"])
            shutil.rmtree(path)
            append_audit(root, plan, "remove-backup-complete", backup["path"])
            removed["backups"].append(backup["path"])
        for source in plan["remove_artifacts"]:
            snapshot = docker.snapshot()
            require(live_identity(snapshot) == plan["live"], "Live API changed during artifact cleanup")
            path = Path(source["path"])
            require(source["kind"] in {"staged_binary", "operator_dir"}, "Unknown release artifact kind")
            binary = source["kind"] == "staged_binary"
            parent = root if binary else OPERATOR_ROOT
            prefix = "new-api-" if binary else "yunshu-api-"
            require(not protected_mount(path, snapshot)
                    and tree_fingerprint(path, parent, prefix=prefix, directory=not binary) == source["fingerprint"],
                    "Release artifact changed or became mounted")
            append_audit(root, plan, "remove-artifact-start", source["path"])
            if binary:
                path.unlink()
            else:
                require(shutil.rmtree.avoids_symlink_attacks, "Platform lacks safe fd-based tree deletion")
                shutil.rmtree(path)
            append_audit(root, plan, "remove-artifact-complete", source["path"])
            removed["artifacts"].append(source["path"])
        for candidate in plan["remove_containers"]:
            snapshot = docker.snapshot()
            require(live_identity(snapshot) == plan["live"], "Live API changed during cleanup")
            matches = [c for c in snapshot["containers"] if c["Id"] == candidate["id"]]
            require(len(matches) == 1 and matches[0].get("Name") == "/" + candidate["name"]
                    and matches[0].get("State", {}).get("Status") == "exited"
                    and matches[0]["Image"] == candidate["image_id"], "Stopped rollback container changed")
            append_audit(root, plan, "remove-container-start", candidate["id"])
            docker.remove_container(candidate["id"])
            append_audit(root, plan, "remove-container-complete", candidate["id"])
            removed["containers"].append(candidate["id"])
        for candidate in plan["remove_image_tags"]:
            snapshot = docker.snapshot()
            require(live_identity(snapshot) == plan["live"], "Live API changed during cleanup")
            require(not any(c["Image"] == candidate["image_id"] for c in snapshot["containers"]),
                    "Image acquired a container reference")
            matches = [i for i in snapshot["images"] if candidate["tag"] in (i.get("RepoTags") or [])]
            require(len(matches) == 1 and matches[0]["Id"] == candidate["image_id"]
                    and all(t.startswith(IMAGE_REPOSITORY + ":") for t in matches[0].get("RepoTags", [])),
                    "Image tag changed or is shared")
            append_audit(root, plan, "remove-image-tag-start", candidate["tag"])
            docker.remove_image_tag(candidate["tag"])
            append_audit(root, plan, "remove-image-tag-complete", candidate["tag"])
            removed["image_tags"].append(candidate["tag"])
        require(live_identity(docker.snapshot()) == plan["live"], "Live API changed during cleanup")
        if registry_path is not None:
            write_registry(registry_path, {**registry, "releases":
                           [r for r in registry["releases"] if r["revision"] in plan["keep_revisions"]]})
        append_audit(root, plan, "complete")
        return {"status": "PASS", "plan_sha256": digest(plan), "removed": removed,
                "capacity": check_capacity(root), "service_restart_commands": 0,
                "volume_removal_commands": 0}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command")
    planning = sub.add_parser("plan")
    planning.add_argument("--registry", required=True)
    planning.add_argument("--output", required=True)
    capacity = sub.add_parser("capacity")
    capacity.add_argument("--growth-bytes", type=int, default=0)
    apply = sub.add_parser("apply")
    for argument in ("registry", "plan", "receipt", "key"):
        apply.add_argument("--" + argument, required=True)
    check = sub.add_parser("check")
    check.add_argument("--registry", required=True)
    check.add_argument("--phase", required=True,
                       choices=["before-backup", "before-build", "after-backup", "before-cutover"])
    check.add_argument("--growth-bytes", type=int)
    check.add_argument("--pending-revision")
    check.add_argument("--pending-backup")
    check.add_argument("--pending-output")
    check.add_argument("--pending-ticket")
    register = sub.add_parser("register")
    register.add_argument("--registry", required=True)
    register.add_argument("--record", required=True)
    inspecting = sub.add_parser("inspect-artifacts")
    inspecting.add_argument("--record", required=True)
    migrating = sub.add_parser("migrate-artifacts")
    migrating.add_argument("--registry", required=True)
    migrating.add_argument("--expected-registry-sha256", required=True)
    migrating.add_argument("--legacy-owned-files")
    migrating.add_argument("--apply", action="store_true")
    signing = sub.add_parser("sign-receipt")
    for argument in ("plan", "verification", "key", "output"):
        signing.add_argument("--" + argument, required=True)
    manifesting = sub.add_parser("archive-manifest")
    manifesting.add_argument("--plan", required=True)
    manifesting.add_argument("--kind", choices=["files", "docker"], required=True)
    manifesting.add_argument("--output", required=True)
    args = parser.parse_args()
    if args.command is None:
        parser.print_help()
        return 0
    if args.command == "sign-receipt":
        receipt = sign_archive_receipt(load_json(args.plan), load_json(args.verification),
                                       private_key(args.key))
        fd = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "w") as stream:
            json.dump(receipt, stream, indent=2, sort_keys=True)
            stream.write("\n")
        emit({"status": "SIGNED", "output": args.output, "plan_sha256": receipt["plan_sha256"]})
        return 0
    if args.command == "archive-manifest":
        plan = load_json(args.plan)
        manifest = files_archive_manifest(plan) if args.kind == "files" else {
            "schema": 1, "image_ids": required_docker_image_ids(plan)}
        require(args.kind == "files" or manifest["image_ids"], "No Docker deletion images need an archive")
        fd = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "w") as stream:
            json.dump(manifest, stream, indent=2, sort_keys=True)
            stream.write("\n")
        emit({"status": "MANIFEST_ONLY", "output": args.output, "plan_sha256": digest(plan)})
        return 0
    require(ROOT.resolve() == ROOT and ROOT.is_dir(), "Exact production instance root is required")
    if args.command == "plan":
        with deployment_lock(ROOT):
            plan = create_plan(ROOT, load_json(args.registry), Docker().snapshot())
        output = Path(args.output)
        with output.open("x") as stream:
            json.dump(plan, stream, indent=2, sort_keys=True)
            stream.write("\n")
        emit({"status": "PLAN_ONLY", "plan_sha256": digest(plan), "output": str(output),
              "keep": plan["keep_revisions"], "backup_candidates": len(plan["remove_backups"]),
              "artifact_candidates": len(plan["remove_artifacts"]),
              "unregistered_artifacts": plan["unregistered_artifacts"],
              "container_candidates": len(plan["remove_containers"]), "tag_candidates": len(plan["remove_image_tags"])})
        return 0
    if args.command == "capacity":
        result = check_capacity(ROOT, args.growth_bytes)
        emit(result)
        return 0 if result["pass"] else 2
    if args.command == "inspect-artifacts":
        with deployment_lock(ROOT):
            artifacts, sources = inspect_release_artifacts(ROOT, load_json(args.record))
        emit({"status": "INSPECT_ONLY", "artifacts": artifacts, "sources": sources})
        return 0
    if args.command == "migrate-artifacts":
        with deployment_lock(ROOT):
            path = Path(args.registry)
            require(path.is_file() and not path.is_symlink()
                    and DIGEST.fullmatch(args.expected_registry_sha256)
                    and hash_file(path) == args.expected_registry_sha256, "Exact existing registry SHA required")
            updated = migrate_release_artifacts(ROOT, load_json(path), Docker().snapshot(),
                                                 load_json(args.legacy_owned_files) if args.legacy_owned_files else None)
            require(hash_file(path) == args.expected_registry_sha256, "Registry changed during migration")
            if args.apply:
                write_registry(path, updated)
        emit({"status": "MIGRATED_METADATA_ONLY" if args.apply else "MIGRATION_PLAN_ONLY",
              "registry": updated, "source_writes": 0})
        return 0
    if args.command == "check":
        with deployment_lock(ROOT):
            registry = load_json(args.registry)
            snapshot = Docker().snapshot()
            preparing = any((args.pending_revision, args.pending_backup, args.pending_output))
            if preparing:
                require(args.phase == "before-backup" and not args.pending_ticket
                        and all((args.pending_revision, args.pending_backup, args.pending_output)),
                        "Pending preparation needs before-backup and all three pending fields")
                result = prepare_pending_release(ROOT, registry, snapshot, args.pending_revision,
                                                 args.pending_backup, args.growth_bytes)
                fd = os.open(args.pending_output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
                with os.fdopen(fd, "w") as stream:
                    json.dump(result["pending_ticket"], stream, indent=2, sort_keys=True)
                    stream.write("\n")
            else:
                require(not args.pending_ticket or args.phase != "before-backup",
                        "A preexisting pending ticket cannot authorize a new backup")
                result = check_release_gate(ROOT, registry, snapshot, args.phase, args.growth_bytes,
                                            pending=load_json(args.pending_ticket) if args.pending_ticket else None)
        emit(result)
        return 0 if result["status"] == "PASS" else 2
    if args.command == "register":
        with deployment_lock(ROOT):
            path = Path(args.registry)
            require(path.is_file() and not path.is_symlink(), "Registry must be an existing regular file")
            updated = register_live_release(ROOT, load_json(path), load_json(args.record), Docker().snapshot())
            write_registry(path, updated)
        emit({"status": "REGISTERED_ARCHIVE_REQUIRED", "head_revision": updated["head_revision"],
              "next_step": "plan, archive off-host, verify, sign-receipt, apply before next backup/build"})
        return 0
    emit(apply_plan(ROOT, load_json(args.registry), load_json(args.plan), load_json(args.receipt),
                    private_key(args.key), Docker(), Path(args.registry)))
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (Refusal, OSError, ValueError, KeyError, subprocess.CalledProcessError) as error:
        print("Retention refused: " + str(error), file=sys.stderr)
        sys.exit(2)
