#!/usr/bin/env python3
"""Deterministic retention contracts; no server, daemon, or provider is contacted."""

import copy
import contextlib
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location("backup_retention", Path(__file__).with_name("backup_retention.py"))
retention = importlib.util.module_from_spec(spec)
spec.loader.exec_module(retention)


class FakeDocker:
    def __init__(self, snapshot):
        self.value = copy.deepcopy(snapshot)
        self.calls = []
        self.before_snapshot = None

    def snapshot(self):
        if self.before_snapshot:
            self.before_snapshot(self)
        return copy.deepcopy(self.value)

    def remove_container(self, container_id):
        self.calls.append(("container-rm", container_id))
        self.value["containers"] = [c for c in self.value["containers"] if c["Id"] != container_id]

    def remove_image_tag(self, tag):
        self.calls.append(("image-rm", tag))
        for image in self.value["images"]:
            if tag in image["RepoTags"]:
                image["RepoTags"].remove(tag)


class RetentionTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = (Path(self.temp.name) / "instance").resolve()
        (self.root / "backup").mkdir(parents=True)
        self.root_patch = patch.object(retention, "ROOT", self.root)
        self.root_patch.start()
        self.addCleanup(self.root_patch.stop)
        self.operator_root = self.root.parent / "operators"
        self.operator_root.mkdir()
        self.operator_patch = patch.object(retention, "OPERATOR_ROOT", self.operator_root)
        self.operator_patch.start()
        self.addCleanup(self.operator_patch.stop)
        self.host_patch = patch.object(retention.socket, "gethostname", return_value="api-server")
        self.host_patch.start()
        self.addCleanup(self.host_patch.stop)
        self.key = b"a private random test key with 32 bytes minimum"
        self.configs = {}
        self.modern_images = {}
        self.archive_count = 0
        self.releases = [self.release(char, day) for char, day in zip("abcd", (22, 21, 20, 19))]
        for index, release in enumerate(self.releases):
            release["parent_revision"] = self.releases[index + 1]["revision"] if index < 3 else None
        self.registry = {"schema": 1, "root": str(self.root), "container": retention.NAME,
                         "rollback_count": 2, "head_revision": self.releases[0]["revision"],
                         "releases": self.releases}
        live = self.container("a", retention.NAME, "running")
        old = self.container("d", retention.NAME + "-rollback-" + self.releases[3]["release"], "exited")
        self.docker = FakeDocker({"containers": [live, old], "images": [self.image(char) for char in "abcd"]})
        self.ample_disk = SimpleNamespace(f_blocks=100, f_bfree=80, f_bavail=80, f_frsize=1024 ** 3)

    def release(self, char, day):
        revision = char * 40
        label = "202610" + str(day) + "-" + revision[:12] + "-010203"
        path = self.root / "backup" / ("release-" + label)
        path.mkdir()
        files = {}
        for component in sorted(retention.COMPONENTS):
            file = path / (component + ".fixture")
            file.write_bytes((component + char).encode())
            files[file.name] = {"bytes": file.stat().st_size, "sha256": retention.hash_file(file)}
        manifest = {"complete": True, "revision": revision,
                    "components": sorted(retention.COMPONENTS), "files": files}
        (path / "retention-manifest.json").write_text(json.dumps(manifest))
        record = {"release": label, "revision": revision, "parent_revision": None, "backup": path.name,
                  "image_id": self.image_id(char), "manifest": "retention-manifest.json",
                  "manifest_sha256": retention.hash_file(path / "retention-manifest.json")}
        staged = self.root / ("new-api-" + label)
        staged.write_bytes(("program" + revision).encode())
        operators = self.operator_root / ("yunshu-api-" + label)
        operators.mkdir()
        (operators / "operator.py").write_text("# immutable operator " + revision)
        (operators / "release.json").write_text(json.dumps({
            "release": label, "source_sha": revision, "base_sha": None,
            "binary_sha256": retention.hash_file(staged),
            "operator_sha256": {"operator.py": retention.hash_file(operators / "operator.py")}}))
        record["artifacts"], _ = retention.inspect_release_artifacts(self.root, record)
        return record

    def discard_release(self, record):
        retention.shutil.rmtree(self.root / "backup" / record["backup"])
        paths = retention.artifact_paths(self.root, record)
        paths["staged_binary"].unlink()
        retention.shutil.rmtree(paths["operator_dir"])

    def image(self, char):
        release = next(r for r in self.releases if r["revision"] == char * 40)
        return {"Id": self.image_id(char),
                "RepoTags": [retention.IMAGE_REPOSITORY + ":" + release["release"]],
                "Config": {"Labels": {"org.opencontainers.image.revision": char * 40}}}

    def container(self, char, name, status):
        return {"Id": char * 64, "Name": "/" + name, "Image": self.image_id(char),
                "Config": {"Labels": {"org.opencontainers.image.revision": char * 40}},
                "State": {"Status": status, "StartedAt": "fixed"},
                "Mounts": [{"Source": str(self.root / "data"), "Destination": "/data"}]}

    def plan(self):
        return retention.create_plan(self.root, self.registry, self.docker.snapshot())

    def receipt(self, plan):
        verification = self.archive_verification(plan)
        with patch.object(retention.socket, "gethostname", return_value="offhost-mac"):
            return retention.sign_archive_receipt(plan, verification, self.key)

    def image_id(self, value):
        config = json.dumps({"rootfs": {"diff_ids": []}, "fixture": value}, sort_keys=True).encode()
        image_id = "sha256:" + hashlib.sha256(config).hexdigest()
        self.configs[image_id] = config.decode()
        return image_id

    def modern_image_id(self, value):
        content = ("modern synthetic layer " + value).encode()
        stream = io.BytesIO()
        with tarfile.open(fileobj=stream, mode="w") as archive:
            member = tarfile.TarInfo("fixture.txt")
            member.size = len(content)
            archive.addfile(member, io.BytesIO(content))
        layer = stream.getvalue()
        layer_sha = hashlib.sha256(layer).hexdigest()
        config = json.dumps({"architecture": "amd64", "os": "linux", "rootfs": {
            "type": "layers", "diff_ids": ["sha256:" + layer_sha]}}, sort_keys=True).encode()
        config_sha = hashlib.sha256(config).hexdigest()
        manifest = json.dumps({"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json",
                               "config": {"digest": "sha256:" + config_sha, "size": len(config),
                                          "mediaType": "application/vnd.oci.image.config.v1+json"},
                               "layers": [{"digest": "sha256:" + layer_sha, "size": len(layer),
                                           "mediaType": "application/vnd.oci.image.layer.v1.tar"}]}, sort_keys=True).encode()
        image_id = "sha256:" + hashlib.sha256(manifest).hexdigest()
        self.modern_images[image_id] = {"manifest": manifest.decode(), "config": config.decode(),
                                        "config_sha": config_sha, "layer": layer.decode(), "layer_sha": layer_sha}
        return image_id

    def archive_verification(self, plan, files_manifest=None, docker_ids=None):
        """Real tiny AES-GCM/gzip/TAR archives, real reviewed Node/Python verifiers."""
        self.archive_count += 1
        job = (Path(self.temp.name) / ("private-job-" + str(self.archive_count))).resolve()
        job.mkdir(mode=0o700)
        key_root = (Path(self.temp.name) / "separate-keys").resolve()
        key_root.mkdir(mode=0o700, exist_ok=True)
        items = []
        kinds = [("files", files_manifest if files_manifest is not None else retention.files_archive_manifest(plan))]
        ids = retention.required_docker_image_ids(plan) if docker_ids is None else docker_ids
        if ids:
            kinds.append(("docker", {"schema": 1, "image_ids": sorted(ids)}))
        writer = r'''
import decimal, io, json, sys, tarfile
kind, expected_path, configs_path = sys.argv[1:]
expected = json.load(open(expected_path))
with tarfile.open(fileobj=sys.stdout.buffer, mode="w|gz", format=tarfile.PAX_FORMAT) as archive:
    if kind == "files":
        for row in expected["entries"]:
            member = tarfile.TarInfo(row["path"])
            member.uid, member.gid, member.mode = row["uid"], row["gid"], row["mode"] & 0o7777
            member.mtime = int(decimal.Decimal(row["mtime_ns"]) / 1000000000)
            member.pax_headers = {"mtime": str(decimal.Decimal(row["mtime_ns"]) / 1000000000)}
            if row["type"] == "dir":
                member.type = tarfile.DIRTYPE
                archive.addfile(member)
            else:
                member.size = row["size"]
                with open("/" + row["path"], "rb") as content:
                    archive.addfile(member, content)
    else:
        dataset = json.load(open(configs_path))
        configs, modern = dataset["legacy"], dataset["modern"]
        manifest = []
        objects = {}
        roots = []
        for image_id in expected["image_ids"]:
            if image_id in modern:
                image = modern[image_id]
                filename = "blobs/sha256/" + image["config_sha"]
                layer_name = "blobs/sha256/" + image["layer_sha"]
                manifest.append({"Config": filename, "Layers": [layer_name], "RepoTags": []})
                objects[filename] = image["config"].encode()
                objects[layer_name] = image["layer"].encode()
                objects["blobs/sha256/" + image_id[7:]] = image["manifest"].encode()
                roots.append({"digest": image_id, "size": len(image["manifest"].encode()),
                              "mediaType": "application/vnd.oci.image.manifest.v1+json"})
            else:
                filename = image_id[7:] + ".json"
                objects[filename] = configs[image_id].encode()
                manifest.append({"Config": filename, "Layers": [], "RepoTags": []})
        if roots:
            objects["index.json"] = json.dumps({"schemaVersion": 2, "manifests": roots}).encode()
        objects["manifest.json"] = json.dumps(manifest).encode()
        for filename, content in objects.items():
            member = tarfile.TarInfo(filename)
            member.size = len(content)
            archive.addfile(member, io.BytesIO(content))
'''
        configs_path = job / "fixture-configs.json"
        configs_path.write_text(json.dumps({"legacy": self.configs, "modern": self.modern_images}))
        configs_path.chmod(0o600)
        for kind, manifest in kinds:
            expected = job / (kind + ".expected.json")
            expected.write_text(json.dumps(manifest))
            expected.chmod(0o600)
            command = job / (kind + ".command.json")
            command.write_text(json.dumps([sys.executable, "-c", writer, kind, str(expected), str(configs_path)]))
            command.chmod(0o600)
            # The exported archive() API accepts a separate private key root;
            # its CLI intentionally uses the user's default, so call the API.
            js = "import {archive} from " + json.dumps(retention.ARCHIVE_CLIENT.as_uri()) + "; "
            options = {"commandJson": str(command), "outputDir": str(job), "name": kind,
                       "kind": kind, "expected": str(expected), "keyRoot": str(key_root)}
            js += "process.stdout.write(JSON.stringify(await archive(" + json.dumps(options) + ")));"
            encrypted = json.loads(subprocess.check_output(["node", "--input-type=module", "-e", js], text=True))
            metadata = Path(encrypted["metadataPath"])
            info = retention.load_json(metadata)
            verified = json.loads(subprocess.check_output(["node", str(retention.ARCHIVE_CLIENT), "verify",
                                                          "--metadata", str(metadata), "--expected", str(expected),
                                                          "--key-file", info["keyLocator"]], text=True))
            items.append({"metadata": str(metadata), "expected": str(expected), "aes_key": info["keyLocator"],
                          "verified_receipt": verified["verificationReceipt"]})
        return {"schema": 2, "plan_sha256": retention.digest(plan), "archives": items}

    def test_plan_uses_parent_chain_preserves_unknown_and_ignores_mtime(self):
        unknown = self.root / "backup" / "sms-special-snapshot"
        unknown.mkdir()
        os.utime(self.root / "backup" / self.releases[3]["backup"], (2000000000, 2000000000))
        plan = self.plan()
        self.assertEqual(plan["keep_revisions"], [c * 40 for c in "abc"])
        self.assertEqual([b["revision"] for b in plan["remove_backups"]], ["d" * 40])
        self.assertEqual(plan["protected_unmanaged"], [unknown.name])
        self.assertEqual({s["revision"] for s in plan["remove_artifacts"]}, {"d" * 40})
        self.assertEqual(len(plan["keep_artifacts"]), 3)
        self.assertEqual(self.docker.calls, [])

    def test_incomplete_tampered_and_linked_backups_refuse(self):
        old = self.root / "backup" / self.releases[3]["backup"]
        with self.subTest("changed file"):
            file = old / "database.fixture"
            original = file.read_bytes()
            file.write_bytes(b"corrupt")
            with self.assertRaises(retention.Refusal):
                self.plan()
            file.write_bytes(original)
        with self.subTest("symlink"):
            (old / "live-db").symlink_to(self.root / "data")
            with self.assertRaises(retention.Refusal):
                self.plan()
            (old / "live-db").unlink()
        with self.subTest("missing complete backup"):
            self.registry["releases"] = self.releases[:2]
            with self.assertRaises(retention.Refusal):
                self.plan()
        self.assertEqual(self.docker.calls, [])

    def test_unrelated_releases_and_parent_cycles_refuse(self):
        self.releases[2]["parent_revision"] = None
        with self.assertRaises(retention.Refusal):
            self.plan()
        self.releases[2]["parent_revision"] = "d" * 40
        self.releases[3]["parent_revision"] = "b" * 40
        with self.assertRaises(retention.Refusal):
            self.plan()

    def test_any_mount_and_running_or_foreign_docker_reference_protected(self):
        old_path = self.root / "backup" / self.releases[3]["backup"]
        self.docker.value["containers"][1]["Mounts"].append({"Source": str(old_path / "database.fixture")})
        with self.assertRaises(retention.Refusal):
            self.plan()
        self.docker.value["containers"][1]["Mounts"].pop()
        foreign = self.container("d", "other-business", "running")
        foreign["Id"] = "f" * 64
        self.docker.value["containers"].append(foreign)
        self.assertEqual(self.plan()["remove_image_tags"], [])
        self.docker.value["containers"].pop()
        self.docker.value["images"][-1]["RepoTags"].append("other-business:shared")
        self.assertEqual(self.plan()["remove_image_tags"], [])
        self.docker.value["containers"][1]["State"]["Status"] = "running"
        self.assertEqual(self.plan()["remove_containers"], [])

    def test_receipt_must_bind_offhost_archive_plan_allowlist_and_expiry(self):
        plan = self.plan()
        receipt = self.receipt(plan)
        retention.verify_receipt(plan, receipt, self.key)
        for field, value in (("expires_at", 0), ("plan_sha256", "f" * 64), ("allowlist", {})):
            with self.subTest(field):
                broken = copy.deepcopy(receipt)
                broken[field] = value
                with self.assertRaises(retention.Refusal):
                    retention.verify_receipt(plan, broken, self.key)
        receipt["expires_at"] = 0
        unsigned = {k: v for k, v in receipt.items() if k != "signature"}
        receipt["signature"] = retention.hmac.new(self.key, retention.canonical(unsigned), hashlib.sha256).hexdigest()
        with self.assertRaises(retention.Refusal):
            retention.verify_receipt(plan, receipt, self.key)

    def test_archive_signing_refuses_same_host_or_unverified_bytes(self):
        plan = self.plan()
        with self.assertRaises(retention.Refusal):
            retention.sign_archive_receipt(plan, {}, self.key)
        with patch.object(retention.socket, "gethostname", return_value="offhost-mac"):
            with self.assertRaises(retention.Refusal):
                retention.sign_archive_receipt(plan, {"verified": True, "source_fingerprints":
                                                      retention.archive_source_fingerprints(plan)}, self.key)

    def test_signing_requires_private_owned_archive_parent_and_exact_locator(self):
        plan = self.plan()
        verification = self.archive_verification(plan)
        metadata = Path(verification["archives"][0]["metadata"])
        archive = metadata.parent / retention.load_json(metadata)["ciphertextFile"]
        alias = metadata.parent / "alias-metadata.json"
        alias.symlink_to(metadata)
        with patch.object(retention.socket, "gethostname", return_value="offhost-mac"):
            for bad in ("public-file", "public-parent", "wrong-owner", "wrong-locator"):
                with self.subTest(bad):
                    archive.chmod(0o644 if bad == "public-file" else 0o600)
                    archive.parent.chmod(0o755 if bad == "public-parent" else 0o700)
                    envelope = copy.deepcopy(verification)
                    if bad == "wrong-locator":
                        envelope["archives"][0]["metadata"] = str(alias)
                    with patch.object(retention.os, "geteuid", return_value=-1 if bad == "wrong-owner" else os.getuid()):
                        with patch.object(retention, "hash_file", wraps=retention.hash_file) as hashing:
                            with self.assertRaises(retention.Refusal):
                                retention.sign_archive_receipt(plan, envelope, self.key)
                            hashing.assert_not_called()
            archive.chmod(0o600)
            archive.parent.chmod(0o700)
            with patch.object(retention, "hash_file", wraps=retention.hash_file) as hashing:
                receipt = retention.sign_archive_receipt(plan, verification, self.key)
                self.assertFalse(any(call.args[0] == archive for call in hashing.call_args_list))
            self.assertEqual(receipt["archives"][0]["privacy"]["parent_mode"], "0700")
            self.assertEqual(receipt["archives"][0]["verification"]["authenticatedReadPasses"], 2)
            broken = copy.deepcopy(receipt)
            del broken["archives"][0]["privacy"]
            broken["signature"] = retention.hmac.new(self.key, retention.canonical(
                {k: v for k, v in broken.items() if k != "signature"}), hashlib.sha256).hexdigest()
            with self.assertRaises(retention.Refusal):
                retention.verify_receipt(plan, broken, self.key)

    def test_signing_requires_current_aes_key_physical_metadata_and_real_gcm(self):
        plan = self.plan()
        envelope = self.archive_verification(plan)
        item = envelope["archives"][0]
        aes_path = Path(item["aes_key"])
        original_key = aes_path.read_bytes()
        metadata_path = Path(item["metadata"])
        original_metadata = metadata_path.read_bytes()
        metadata = retention.load_json(metadata_path)
        ciphertext = metadata_path.parent / metadata["ciphertextFile"]
        original_ciphertext = ciphertext.read_bytes()
        proof_path = Path(item["verified_receipt"])
        original_proof = proof_path.read_bytes()
        with patch.object(retention.socket, "gethostname", return_value="offhost-mac"):
            aes_path.unlink()
            with self.assertRaises((retention.Refusal, OSError)):
                retention.sign_archive_receipt(plan, envelope, self.key)
            aes_path.write_bytes(b"x" * 32)
            aes_path.chmod(0o600)
            with self.assertRaisesRegex(retention.Refusal, "current AES key"):
                retention.sign_archive_receipt(plan, envelope, self.key)
            aes_path.write_bytes(original_key)
            changed_metadata = {**metadata, "childExitCode": 7}
            metadata_path.write_text(json.dumps(changed_metadata))
            with self.assertRaisesRegex(retention.Refusal, "Physical metadata"):
                retention.sign_archive_receipt(plan, envelope, self.key)
            metadata_path.write_bytes(original_metadata)
            # A private, nonempty, same-size counterfeit plus a self-described
            # VERIFIED receipt cannot replace authentication of actual bytes.
            counterfeit = bytes([original_ciphertext[0] ^ 1]) + original_ciphertext[1:]
            ciphertext.write_bytes(counterfeit)
            forged_metadata = copy.deepcopy(metadata)
            forged_metadata["ciphertext"]["sha256"] = hashlib.sha256(counterfeit).hexdigest()
            metadata_path.write_text(json.dumps(forged_metadata))
            forged_proof = json.loads(original_proof)
            forged_proof["metadataSha256"] = retention.hash_file(metadata_path)
            forged_proof["ciphertext"] = forged_metadata["ciphertext"]
            proof_path.write_text(json.dumps(forged_proof))
            with self.assertRaisesRegex(retention.Refusal, "Archive client verification failed"):
                retention.sign_archive_receipt(plan, envelope, self.key)
            metadata_path.write_bytes(original_metadata)
            proof_path.write_bytes(original_proof)
            ciphertext.write_bytes(original_ciphertext)
            receipt = retention.sign_archive_receipt(plan, envelope, self.key)
        self.assertEqual(receipt["archives"][0]["aes_key_sha256"], hashlib.sha256(original_key).hexdigest())
        self.assertEqual(self.docker.calls, [])

    def test_signing_requires_every_docker_image_real_proof_including_seventeen(self):
        plan = self.plan()
        for index in range(16):
            image_id = self.image_id("additional-fixture-" + str(index))
            plan["remove_image_tags"].append({"tag": retention.IMAGE_REPOSITORY + ":fixture-" + str(index),
                                               "image_id": image_id, "revision": "d" * 40})
        required = retention.required_docker_image_ids(plan)
        self.assertEqual(len(required), 17)
        complete = self.archive_verification(plan)
        with patch.object(retention.socket, "gethostname", return_value="offhost-mac"):
            receipt = retention.sign_archive_receipt(plan, complete, self.key)
            docker = [a for a in receipt["archives"] if a["kind"] == "docker"][0]
            self.assertEqual(docker["verification"]["tar"]["image_count"], 17)
            self.assertEqual(docker["image_ids"], required)
            missing_docker = {**complete, "archives": complete["archives"][:1]}
            with self.assertRaisesRegex(retention.Refusal, "every distinct Docker"):
                retention.sign_archive_receipt(plan, missing_docker, self.key)
            subset = self.archive_verification(plan, docker_ids=required[:-1])
            with self.assertRaisesRegex(retention.Refusal, "every distinct Docker"):
                retention.sign_archive_receipt(plan, subset, self.key)

    def test_modern_manifest_image_id_signs_and_cleans_only_after_real_oci_tar_proof(self):
        modern_id = self.modern_image_id("obsolete Docker 29 fixture")
        self.releases[3]["image_id"] = modern_id
        self.docker.value["containers"][1]["Image"] = modern_id
        self.docker.value["images"][-1]["Id"] = modern_id
        plan = self.plan()
        self.assertEqual(retention.required_docker_image_ids(plan), [modern_id])
        receipt = self.receipt(plan)
        docker_proof = [item for item in receipt["archives"] if item["kind"] == "docker"][0]
        self.assertEqual(docker_proof["verification"]["tar"]["image_ids"], [modern_id])
        self.assertNotEqual(modern_id, "sha256:" + self.modern_images[modern_id]["config_sha"])
        with patch.object(retention, "check_capacity", return_value={"pass": True}):
            result = retention.apply_plan(self.root, self.registry, plan, receipt, self.key, self.docker)
        self.assertEqual(result["status"], "PASS")
        self.assertEqual(self.docker.calls[0], ("container-rm", "d" * 64))
        self.assertTrue(all((self.root / source["path"]).is_dir() for source in plan["keep_backups"]))

    def test_apply_removes_only_signed_obsolete_set_and_updates_registry(self):
        plan = self.plan()
        receipt = self.receipt(plan)
        registry_path = self.root / "retention-registry.json"
        registry_path.write_text(json.dumps(self.registry))
        with patch.object(retention, "check_capacity", return_value={"pass": True}):
            result = retention.apply_plan(self.root, self.registry, plan, receipt, self.key, self.docker, registry_path)
        self.assertEqual(result["status"], "PASS")
        self.assertFalse((self.root / plan["remove_backups"][0]["path"]).exists())
        self.assertEqual(len(result["removed"]["artifacts"]), 2)
        self.assertTrue(all(not Path(s["path"]).exists() for s in plan["remove_artifacts"]))
        self.assertTrue(all((self.root / b["path"]).is_dir() for b in plan["keep_backups"]))
        self.assertEqual(self.docker.calls, [("container-rm", "d" * 64),
                         ("image-rm", retention.IMAGE_REPOSITORY + ":" + self.releases[3]["release"])])
        cleaned = retention.load_json(registry_path)
        self.assertEqual(len(cleaned["releases"]), 3)
        self.assertEqual(retention.create_plan(self.root, cleaned, self.docker.snapshot())["remove_backups"], [])
        self.assertEqual(result["service_restart_commands"], 0)
        self.assertEqual(result["volume_removal_commands"], 0)

    def test_changed_source_live_or_busy_deploy_lock_refuses_before_mutation(self):
        for mode in ("source", "live", "lock"):
            with self.subTest(mode):
                plan = self.plan()
                receipt = self.receipt(plan)
                if mode == "source":
                    file = self.root / plan["remove_backups"][0]["path"] / "new-file"
                    file.write_text("new data")
                elif mode == "live":
                    self.docker.value["containers"][0]["State"]["StartedAt"] = "restarted"
                else:
                    lock = (self.root / ".deploy.lock").open("a")
                    retention.fcntl.flock(lock, retention.fcntl.LOCK_EX | retention.fcntl.LOCK_NB)
                with self.assertRaises((retention.Refusal, BlockingIOError)):
                    retention.apply_plan(self.root, self.registry, plan, receipt, self.key, self.docker)
                if mode == "source":
                    file.unlink()
                elif mode == "live":
                    self.docker.value["containers"][0]["State"]["StartedAt"] = "fixed"
                else:
                    lock.close()
        self.assertEqual(self.docker.calls, [])

    def test_hardlink_cleanup_keeps_retained_content_and_fingerprint(self):
        retained = self.root / "backup" / self.releases[0]["backup"]
        obsolete = self.root / "backup" / self.releases[3]["backup"]
        old_file = obsolete / "binary.fixture"
        old_file.unlink()
        os.link(retained / "binary.fixture", old_file)
        manifest_path = obsolete / "retention-manifest.json"
        manifest = retention.load_json(manifest_path)
        manifest["files"]["binary.fixture"] = {"bytes": old_file.stat().st_size,
                                                 "sha256": retention.hash_file(old_file)}
        manifest_path.write_text(json.dumps(manifest))
        self.releases[3]["manifest_sha256"] = retention.hash_file(manifest_path)
        plan = self.plan()
        receipt = self.receipt(plan)
        before = retention.tree_fingerprint(retained, self.root / "backup")
        with patch.object(retention, "check_capacity", return_value={"pass": True}):
            retention.apply_plan(self.root, self.registry, plan, receipt, self.key, self.docker)
        self.assertEqual(retention.tree_fingerprint(retained, self.root / "backup"), before)
        self.assertEqual((retained / "binary.fixture").read_bytes(), b"binarya")

    def test_live_change_between_deletions_stops_without_docker_mutation(self):
        plan = self.plan()
        receipt = self.receipt(plan)
        count = [0]

        def change_live(docker):
            count[0] += 1
            if count[0] == 3:
                docker.value["containers"][0]["State"]["StartedAt"] = "unexpected-new-process"

        self.docker.before_snapshot = change_live
        with self.assertRaises(retention.Refusal):
            retention.apply_plan(self.root, self.registry, plan, receipt, self.key, self.docker)
        self.assertEqual(self.docker.calls, [])
        self.assertTrue(all((self.root / b["path"]).is_dir() for b in plan["keep_backups"]))
        audit = (self.root / ".backup-retention-audit.jsonl").read_text()
        self.assertIn("remove-backup-complete", audit)
        self.assertNotIn('"event":"complete"', audit)

    def test_live_mount_reordering_preserves_identity_but_actual_changes_refuse(self):
        live = self.docker.value["containers"][0]
        live["Mounts"] = [
            {"Type": "bind", "Source": str(self.root / "data"), "Destination": "/data",
             "Mode": "rw", "RW": True, "Propagation": "rprivate"},
            {"Type": "volume", "Name": "retained-volume", "Source": "/var/lib/docker/volumes/retained/_data",
             "Destination": "/var/lib/postgresql/data", "Driver": "local", "Mode": "",
             "RW": True, "Propagation": ""}]
        live["Config"]["Env"] = ["SECOND=2", "FIRST=1"]
        original = self.docker.snapshot()
        identity = retention.live_identity(original)
        self.assertEqual(original, self.docker.snapshot())
        baseline = copy.deepcopy(live["Mounts"])
        live["Mounts"] = list(reversed(baseline))
        reordered = self.docker.snapshot()
        self.assertEqual(retention.live_identity(reordered), identity)
        self.assertEqual(reordered, self.docker.snapshot())
        self.assertEqual(live["Config"]["Env"], ["SECOND=2", "FIRST=1"])
        live["Mounts"].append(copy.deepcopy(live["Mounts"][0]))
        self.assertNotEqual(retention.live_identity(self.docker.snapshot()), identity)
        live["Mounts"] = copy.deepcopy(baseline)
        plan = self.plan()
        receipt = self.receipt(plan)
        for field, changed in (("Source", str(self.root / "different-data")),
                               ("Destination", "/different-data"), ("RW", False), ("Propagation", "rshared")):
            with self.subTest(field):
                live["Mounts"][0][field] = changed
                self.assertNotEqual(retention.live_identity(self.docker.snapshot()), identity)
                with self.assertRaises(retention.Refusal):
                    retention.apply_plan(self.root, self.registry, plan, receipt, self.key, self.docker)
                self.assertEqual(self.docker.calls, [])
                live["Mounts"] = copy.deepcopy(baseline)
        live["Mounts"] = list(reversed(baseline))
        with patch.object(retention, "check_capacity", return_value={"pass": True}):
            result = retention.apply_plan(self.root, self.registry, plan, receipt, self.key, self.docker)
        self.assertEqual(result["status"], "PASS")
        self.assertEqual(live["Config"]["Env"], ["SECOND=2", "FIRST=1"])

    def test_capacity_budget_and_quantity_gate_block_next_large_operation(self):
        safe = SimpleNamespace(f_blocks=10, f_bfree=4, f_bavail=4, f_frsize=1024 ** 3)
        self.assertTrue(retention.check_capacity(self.root, 1024 ** 3, safe)["pass"])
        self.assertFalse(retention.check_capacity(self.root, 1024 ** 3 + 1, safe)["pass"])
        threshold = SimpleNamespace(f_blocks=100, f_bfree=7, f_bavail=7, f_frsize=1024 ** 3)
        self.assertFalse(retention.check_capacity(self.root, 0, threshold)["pass"])
        with self.assertRaises(retention.Refusal):
            retention.check_capacity(self.root, -1, safe)
        before = retention.check_release_gate(self.root, self.registry, self.docker.snapshot(),
                                              "before-build", 1, self.ample_disk)
        after = retention.check_release_gate(self.root, self.registry, self.docker.snapshot(),
                                             "before-cutover", 0, self.ample_disk)
        self.assertEqual(before["status"], "BLOCKED")
        self.assertEqual(after["status"], "BLOCKED")
        self.assertEqual(retention.check_release_gate(self.root, self.registry, self.docker.snapshot(),
                                                     "after-backup", 0, self.ample_disk)["status"], "PASS")
        for phase in ("before-backup", "before-build"):
            for missing in (None, 0):
                with self.subTest(phase=phase, budget=missing):
                    with self.assertRaises(retention.Refusal):
                        retention.check_release_gate(self.root, self.registry, self.docker.snapshot(),
                                                     phase, missing, self.ample_disk)

    def test_release_cycle_pending_backup_build_register_archive_returns_to_three(self):
        self.registry = {**self.registry, "releases": self.releases[:3]}
        self.discard_release(self.releases[3])
        revision = "e" * 40
        backup = "release-20261023-" + revision[:12] + "-010203"
        before = retention.prepare_pending_release(self.root, self.registry, self.docker.snapshot(),
                                                    revision, backup, 1024, self.ample_disk)
        ticket = before["pending_ticket"]
        self.assertEqual(before["status"], "PASS")
        new = self.release("e", 23)
        new["parent_revision"] = "a" * 40
        with self.assertRaises(retention.Refusal):
            retention.check_release_gate(self.root, self.registry, self.docker.snapshot(),
                                         "before-build", 1024, self.ample_disk, ticket)
        op_info_path = retention.artifact_paths(self.root, new)["operator_dir"] / "release.json"
        op_info = retention.load_json(op_info_path)
        op_info.update({"base_sha": "a" * 40, "retention_ticket_sha256": retention.digest(ticket)})
        op_info_path.write_text(json.dumps(op_info))
        new["artifacts"], _ = retention.inspect_release_artifacts(self.root, new)
        manifest_path = self.root / "backup" / backup / "retention-manifest.json"
        manifest = retention.load_json(manifest_path)
        manifest["pending_release"] = {"ticket_sha256": retention.digest(ticket),
                                       "pending_revision": revision, "parent_revision": "a" * 40,
                                       "live": ticket["live"]}
        manifest_path.write_text(json.dumps(manifest))
        new["manifest_sha256"] = retention.hash_file(manifest_path)
        without_ticket = retention.check_release_gate(self.root, self.registry, self.docker.snapshot(),
                                                       "before-build", 1024, self.ample_disk)
        self.assertEqual(without_ticket["status"], "BLOCKED")
        with self.assertRaises(retention.Refusal):
            retention.check_release_gate(self.root, self.registry, self.docker.snapshot(), "before-build",
                                         1024, self.ample_disk, ticket, ticket["expires_at"] + 1)
        with self.assertRaises(retention.Refusal):
            retention.prepare_pending_release(self.root, self.registry, self.docker.snapshot(),
                                               revision, backup, 1024, self.ample_disk)
        wrong_parent = {**ticket, "parent_revision": "f" * 40}
        with self.assertRaises(retention.Refusal):
            retention.check_release_gate(self.root, self.registry, self.docker.snapshot(),
                                         "before-build", 1024, self.ample_disk, wrong_parent)
        original_binding = manifest["pending_release"]
        manifest["pending_release"] = {**original_binding, "live": {**ticket["live"], "image_id": "wrong-image"}}
        manifest_path.write_text(json.dumps(manifest))
        with self.assertRaises(retention.Refusal):
            retention.check_release_gate(self.root, self.registry, self.docker.snapshot(),
                                         "before-build", 1024, self.ample_disk, ticket)
        manifest["pending_release"] = original_binding
        manifest_path.write_text(json.dumps(manifest))
        extra = self.root / "backup" / ("release-20261024-" + "f" * 12 + "-010203")
        extra.mkdir()
        with self.assertRaises(retention.Refusal):
            retention.check_release_gate(self.root, self.registry, self.docker.snapshot(),
                                         "before-build", 1024, self.ample_disk, ticket)
        extra.rmdir()
        stray = self.operator_root / ("yunshu-api-20261024-" + "f" * 12 + "-010203")
        stray.mkdir()
        with self.assertRaises(retention.Refusal):
            retention.check_release_gate(self.root, self.registry, self.docker.snapshot(),
                                         "before-build", 1024, self.ample_disk, ticket)
        stray.rmdir()
        # Its exact operators may exist before the binary build. The stage file
        # appears later and must then match that release's declared binary SHA.
        staged = retention.artifact_paths(self.root, new)["staged_binary"]
        staged_bytes = staged.read_bytes()
        staged.unlink()
        prebuild = retention.check_release_gate(self.root, self.registry, self.docker.snapshot(),
                                                "before-build", 1024, self.ample_disk, ticket)
        self.assertEqual(prebuild["status"], "PASS")
        with self.assertRaisesRegex(retention.Refusal, "both complete"):
            retention.check_release_gate(self.root, self.registry, self.docker.snapshot(),
                                         "before-cutover", 0, self.ample_disk, ticket)
        staged.write_bytes(b"wrong candidate program")
        with self.assertRaises(retention.Refusal):
            retention.check_release_gate(self.root, self.registry, self.docker.snapshot(),
                                         "before-cutover", 0, self.ample_disk, ticket)
        staged.write_bytes(staged_bytes)
        operators = retention.artifact_paths(self.root, new)["operator_dir"]
        missing_operators = operators.with_name("temporarily-missing-operators")
        operators.rename(missing_operators)
        with self.assertRaisesRegex(retention.Refusal, "both complete"):
            retention.check_release_gate(self.root, self.registry, self.docker.snapshot(),
                                         "before-cutover", 0, self.ample_disk, ticket)
        missing_operators.rename(operators)
        frozen = operators / "operator.py"
        original_frozen = frozen.read_bytes()
        frozen.write_text("wrong frozen source")
        with self.assertRaisesRegex(retention.Refusal, "Frozen operator source"):
            retention.check_release_gate(self.root, self.registry, self.docker.snapshot(),
                                         "before-cutover", 0, self.ample_disk, ticket)
        frozen.write_bytes(original_frozen)
        for phase, budget in (("before-build", 1024), ("after-backup", 0), ("before-cutover", 0)):
            result = retention.check_release_gate(self.root, self.registry, self.docker.snapshot(),
                                                  phase, budget, self.ample_disk, ticket)
            self.assertEqual(result["status"], "PASS")
        # Complete the simulated cutover, then register its exact live identity.
        self.docker.value["containers"][0] = self.container("e", retention.NAME, "running")
        self.registry = retention.register_live_release(self.root, self.registry, new, self.docker.snapshot())
        blocked = retention.check_release_gate(self.root, self.registry, self.docker.snapshot(),
                                               "before-backup", 1024, self.ample_disk)
        self.assertEqual(blocked["status"], "BLOCKED")
        plan = self.plan()
        self.assertEqual(len(plan["remove_backups"]), 1)
        receipt = self.receipt(plan)
        registry_path = self.root / "retention-registry.json"
        registry_path.write_text(json.dumps(self.registry))
        with patch.object(retention, "check_capacity", return_value={"pass": True}):
            retention.apply_plan(self.root, self.registry, plan, receipt, self.key, self.docker, registry_path)
        cleaned = retention.load_json(registry_path)
        ready = retention.check_release_gate(self.root, cleaned, self.docker.snapshot(),
                                             "before-backup", 1024, self.ample_disk)
        self.assertEqual(ready["status"], "PASS")
        self.assertEqual(len(cleaned["releases"]), 3)
        self.assertTrue(all(Path(a[k]["path"]).exists() for a in plan["keep_artifacts"]
                            for k in ("staged_binary", "operator_dir")))

    def test_artifacts_need_archive_coverage_and_reject_source_drift(self):
        plan = self.plan()
        partial = retention.files_archive_manifest(plan)
        prefixes = [Path(source["path"]).as_posix().lstrip("/") for source in plan["remove_artifacts"]]
        partial["entries"] = [entry for entry in partial["entries"]
                              if not any(entry["path"] == p or entry["path"].startswith(p + "/") for p in prefixes)]
        partial["tree_sha256"] = hashlib.sha256(json.dumps(partial["entries"], ensure_ascii=False,
                                                            sort_keys=True, separators=(",", ":")).encode()).hexdigest()
        proof = self.archive_verification(plan, files_manifest=partial)
        with patch.object(retention.socket, "gethostname", return_value="offhost-mac"):
            with self.assertRaises(retention.Refusal):
                retention.sign_archive_receipt(plan, proof, self.key)
        receipt = self.receipt(plan)
        old_ops = retention.artifact_paths(self.root, self.releases[3])["operator_dir"]
        (old_ops / "run.log").write_text("changed after archive")
        with self.assertRaises(retention.Refusal):
            retention.apply_plan(self.root, self.registry, plan, receipt, self.key, self.docker)
        self.assertTrue((self.root / plan["remove_backups"][0]["path"]).exists())
        self.assertEqual(self.docker.calls, [])
        refreshed = self.plan()
        current_ops = retention.artifact_paths(self.root, self.releases[0])["operator_dir"]
        (current_ops / "run.log").write_text("current retained operator logs may evolve")
        self.assertEqual(self.plan()["keep_artifacts"], refreshed["keep_artifacts"])
        frozen_file = old_ops / "operator.py"
        original = frozen_file.read_bytes()
        frozen_file.write_text("tampered frozen source")
        with self.assertRaises(retention.Refusal):
            self.plan()
        frozen_file.write_bytes(original)

    def test_artifact_mounts_unknown_scopes_and_false_matches_protected(self):
        paths = retention.artifact_paths(self.root, self.releases[3])
        for source in (paths["staged_binary"], paths["staged_binary"].parent,
                       paths["operator_dir"], paths["operator_dir"] / "operator.py", self.operator_root):
            with self.subTest(mount=str(source)):
                self.docker.value["containers"][1]["Mounts"].append({"Source": str(source)})
                with self.assertRaises(retention.Refusal):
                    self.plan()
                self.docker.value["containers"][1]["Mounts"].pop()
        foreign = self.operator_root / "yunshu-api-other-business"
        foreign.mkdir()
        (foreign / "customer.db").write_text("never selected")
        unknown = self.root / ("new-api-20261024-" + "f" * 12 + "-010203")
        unknown.write_text("unregistered exact release stage")
        plan = self.plan()
        self.assertEqual(plan["unregistered_artifacts"], [str(unknown)])
        self.assertNotIn(str(foreign), [s["path"] for s in plan["remove_artifacts"]])
        receipt = self.receipt(plan)
        with patch.object(retention, "check_capacity", return_value={"pass": True}):
            retention.apply_plan(self.root, self.registry, plan, receipt, self.key, self.docker)
        self.assertEqual((foreign / "customer.db").read_text(), "never selected")
        self.assertTrue(unknown.exists())
        cleaned = {**self.registry, "releases": self.releases[:3]}
        before = retention.check_release_gate(self.root, cleaned, self.docker.snapshot(),
                                              "before-build", 1, self.ample_disk)
        self.assertEqual(before["status"], "BLOCKED")
        self.assertEqual(before["unarchived_obsolete_backups"], [])
        self.assertEqual(before["unregistered_artifacts"], [str(unknown)])

    def test_existing_three_migrate_explicit_legacy_files_without_source_writes(self):
        self.discard_release(self.releases[3])
        registry = {**self.registry, "releases": copy.deepcopy(self.releases[:3])}
        legacy = {}
        for record in registry["releases"]:
            record.pop("artifacts")
            if record["revision"] == "a" * 40:
                continue
            operators = retention.artifact_paths(self.root, record)["operator_dir"]
            info = retention.load_json(operators / "release.json")
            info.pop("operator_sha256")
            (operators / "release.json").write_text(json.dumps(info))
            names = ["release.json", "deploy-preserving-instance.py", "rollback-instance.py", "backup-instance.py",
                     "prepare-isolated-frontend.py", "verify-frontend-production.py", "probe-embedded-ui.py",
                     "report-fixture-contract.json", "frontend-dist-manifest.json"]
            for name in names[1:]:
                (operators / name).write_text("owned historical source " + name)
            legacy[record["revision"]] = names
        before = {str(path): retention.tree_fingerprint(path, self.operator_root, prefix="yunshu-api-")
                  for path in self.operator_root.iterdir()}
        with self.assertRaises(retention.Refusal):
            retention.migrate_release_artifacts(self.root, registry, self.docker.snapshot())
        migrated = retention.migrate_release_artifacts(self.root, registry, self.docker.snapshot(), legacy)
        self.assertEqual(migrated["releases"][0]["artifacts"]["operator_dir"]["manifest_origin"], "release-operator_sha256")
        self.assertTrue(all(r["artifacts"]["operator_dir"]["manifest_origin"] == "explicit-legacy-owned-files"
                            for r in migrated["releases"][1:]))
        after = {str(path): retention.tree_fingerprint(path, self.operator_root, prefix="yunshu-api-")
                 for path in self.operator_root.iterdir()}
        self.assertEqual(after, before)
        self.assertEqual(self.docker.calls, [])
        self.assertEqual(retention.create_plan(self.root, migrated, self.docker.snapshot())["remove_artifacts"], [])
        registry_path = self.root / "retention-registry.json"
        registry_path.write_text(json.dumps(registry))
        legacy_path = self.root / "legacy-owned-files.json"
        legacy_path.write_text(json.dumps(legacy))
        cli = ["backup_retention.py", "migrate-artifacts", "--registry", str(registry_path),
               "--expected-registry-sha256", retention.hash_file(registry_path),
               "--legacy-owned-files", str(legacy_path), "--apply"]
        with patch.object(retention.sys, "argv", cli), patch.object(retention, "Docker", return_value=self.docker):
            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                self.assertEqual(retention.main(), 0)
        self.assertEqual(json.loads(output.getvalue())["source_writes"], 0)
        self.assertEqual(retention.load_json(registry_path), migrated)
        self.assertEqual({str(path): retention.tree_fingerprint(path, self.operator_root, prefix="yunshu-api-")
                          for path in self.operator_root.iterdir()}, before)

    def test_register_requires_exact_live_parent_and_clean_registry(self):
        cleaned = {**self.registry, "releases": self.releases[:3]}
        # A previously cleaned instance has only its three retained directories.
        self.discard_release(self.releases[3])
        new = self.release("e", 23)
        new["parent_revision"] = "a" * 40
        self.docker.value["containers"][0] = self.container("e", retention.NAME, "running")
        self.docker.value["images"].append({"Id": new["image_id"], "RepoTags": [],
                                             "Config": {"Labels": {"org.opencontainers.image.revision": "e" * 40}}})
        updated = retention.register_live_release(self.root, cleaned, new, self.docker.snapshot())
        self.assertEqual(updated["head_revision"], "e" * 40)
        self.assertEqual(len(updated["releases"]), 4)
        artifacts = new.pop("artifacts")
        with self.assertRaises(retention.Refusal):
            retention.register_live_release(self.root, cleaned, new, self.docker.snapshot())
        new["artifacts"] = artifacts
        new["artifacts"]["operator_dir"]["manifest_origin"] = "explicit-legacy-owned-files"
        with self.assertRaises(retention.Refusal):
            retention.register_live_release(self.root, cleaned, new, self.docker.snapshot())
        new["artifacts"]["operator_dir"]["manifest_origin"] = "release-operator_sha256"
        new["parent_revision"] = "f" * 40
        with self.assertRaises(retention.Refusal):
            retention.register_live_release(self.root, cleaned, new, self.docker.snapshot())


if __name__ == "__main__":
    unittest.main()
