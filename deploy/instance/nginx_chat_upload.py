#!/usr/bin/env python3
"""Plan and CAS-install the scoped harness chat body-upload timeout.

Planning is read-only for nginx. Applying requires explicit --reload and a reviewed
plan directory; syntax failure restores the exact original files before returning.
"""
from __future__ import annotations

import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile


MARKER = "# BEGIN managed harness chat upload v1"
RELAY = "location ~ ^/harness/(v1|v1beta|v1beta1|v2|pg|mj|suno|relay)(/|$) {"
METRICS = '''# Managed harness chat upload diagnostics: http context only.
# Only bounded numeric Content-Length is exposed, never arbitrary header text.
map $http_content_length $yunshu_harness_chat_content_length {
    default "";
    "~^[0-9]{1,19}$" $http_content_length;
}
log_format yunshu_harness_chat_metrics escape=json
    '{"at":"$time_iso8601","route":"chat_completions",'
    '"request_id":"$request_id","status":$status,'
    '"request_length":$request_length,"content_length":"$yunshu_harness_chat_content_length",'
    '"request_complete":"$request_completion","request_time":$request_time,'
    '"body_bytes_sent":$body_bytes_sent,"upstream_status":"$upstream_status",'
    '"upstream_response_time":"$upstream_response_time",'
    '"upstream_connect_time":"$upstream_connect_time",'
    '"upstream_header_time":"$upstream_header_time",'
    '"connection":$connection,"connection_requests":$connection_requests}';
'''


def digest(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def snapshot(path: Path) -> dict:
    if path.is_symlink():
        raise ValueError("refusing symlink configuration target")
    if not path.exists():
        return {"exists": False}
    stat = path.stat()
    if not path.is_file():
        raise ValueError("configuration target is not a regular file")
    return {"exists": True, "sha256": digest(path.read_bytes()),
            "mode": stat.st_mode & 0o7777, "uid": stat.st_uid, "gid": stat.st_gid}


def atomic_write(path: Path, data: bytes, metadata: dict) -> None:
    fd, temporary = tempfile.mkstemp(prefix="." + path.name + ".", dir=path.parent)
    try:
        with os.fdopen(fd, "wb") as stream:
            stream.write(data)
            stream.flush()
            os.fchmod(stream.fileno(), metadata["mode"])
            if os.geteuid() == 0:
                os.fchown(stream.fileno(), metadata["uid"], metadata["gid"])
            os.fsync(stream.fileno())
        os.replace(temporary, path)
        directory_fd = os.open(path.parent, os.O_RDONLY)
        try:
            os.fsync(directory_fd)
        finally:
            os.close(directory_fd)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def master_pid(path: Path | None) -> int | None:
    if path is None:
        return None
    value = path.read_text().strip()
    if not value.isdecimal() or int(value) <= 1:
        raise ValueError("invalid nginx master PID file")
    pid = int(value)
    os.kill(pid, 0)
    return pid


def candidate_snippet(original: bytes) -> bytes:
    text = original.decode("utf-8")
    if MARKER in text or "yunshu-harness-chat-metrics.log" in text:
        raise ValueError("managed chat location already present")
    if text.count(RELAY) != 1:
        raise ValueError("expected exactly one known harness relay location")
    start = text.index(RELAY)
    body_start = start + len(RELAY)
    # The source relay block must remain simple; reject nested blocks/quotes with
    # braces rather than guessing a boundary and modifying unrelated locations.
    end = text.find("\n}", body_start)
    if end < 0:
        raise ValueError("relay block end missing")
    body = text[body_start:end]
    if "{" in body or "}" in body:
        raise ValueError("relay block needs manual review of nested braces")
    required = ["limit_req zone=yunshu_harness burst=100 nodelay;",
                "limit_req_status 429;", "client_max_body_size 100m;",
                "rewrite ^/harness/(.*)$ /$1 break;", "proxy_pass http://127.0.0.1:3010;",
                "proxy_connect_timeout 5s;", "proxy_send_timeout 3600s;",
                "proxy_read_timeout 3600s;", "proxy_buffering off;"]
    if any(body.count(line) != 1 for line in required):
        raise ValueError("relay safeguards changed; manual candidate review required")
    if re.search(r"\b(client_body_timeout|proxy_request_buffering|proxy_next_upstream)\s", body):
        raise ValueError("relay request-body or retry setting requires manual review")
    block = (MARKER + "\n"
             "# 120s between body reads; request buffering and single POST forwarding remain enabled.\n"
             "location ~ ^/harness/v1/chat/completions/?$ {" + body + "\n"
             "    client_body_timeout 120s;\n"
             "    proxy_request_buffering on;\n"
             "    proxy_next_upstream off;\n"
             "    access_log /var/log/nginx/yunshu-harness-chat-metrics.log yunshu_harness_chat_metrics;\n"
             "}\n# END managed harness chat upload v1\n\n")
    return (text[:start] + block + text[start:]).encode()


def make_plan(snippet: Path, http_conf: Path, output: Path, expected_sha256: str) -> dict:
    snippet = snippet.absolute()
    http_conf = http_conf.absolute()
    old = snapshot(snippet)
    if old.get("sha256") != expected_sha256:
        raise ValueError("snippet SHA256 differs from reviewed baseline")
    if snapshot(http_conf)["exists"]:
        raise ValueError("new http configuration target must not already exist")
    if not http_conf.parent.is_dir():
        raise ValueError("http configuration parent missing")
    original = snippet.read_bytes()
    candidate = candidate_snippet(original)
    metrics = METRICS.encode()
    output.mkdir(mode=0o700, parents=True, exist_ok=False)
    files = [{"path": str(snippet), "before": old, "before_file": "snippet.before.conf",
              "after_file": "snippet.after.conf", "after_sha256": digest(candidate)},
             {"path": str(http_conf), "before": {"exists": False},
              "after_file": "metrics.after.conf", "after_sha256": digest(metrics)}]
    (output / "snippet.before.conf").write_bytes(original)
    (output / "snippet.after.conf").write_bytes(candidate)
    (output / "metrics.after.conf").write_bytes(metrics)
    plan = {"schema": 1, "files": files, "body_timeout_seconds": 120,
            "scope": "/harness/v1/chat/completions with optional trailing slash",
            "production_changed": False}
    (output / "plan.json").write_text(json.dumps(plan, indent=2) + "\n")
    for path in output.iterdir():
        path.chmod(0o600)
    return plan


def apply_plan(directory: Path, lock: Path, nginx: str,
               config: str | None = None, prefix: str | None = None,
               master_pid_file: Path | None = None) -> dict:
    # Config source, exact before bytes and candidate hashes are reviewable in
    # the plan; never trust caller-supplied inline replacement text at apply time.
    plan = json.loads((directory / "plan.json").read_text())
    if plan.get("schema") != 1 or len(plan.get("files", [])) != 2:
        raise ValueError("unsupported plan")
    files = plan["files"]
    for entry in files:
        data = (directory / entry["after_file"]).read_bytes()
        if digest(data) != entry["after_sha256"]:
            raise ValueError("candidate content changed after review")
        if entry["before"]["exists"]:
            before = (directory / entry["before_file"]).read_bytes()
            if digest(before) != entry["before"]["sha256"]:
                raise ValueError("original backup changed after review")
    command = [nginx]
    if prefix:
        command += ["-p", prefix]
    if config:
        command += ["-c", config]
    receipt = directory / "apply-result.json"
    with lock.open("a") as lock_file:
        fcntl.flock(lock_file, fcntl.LOCK_EX | fcntl.LOCK_NB)
        current = [snapshot(Path(item["path"])) for item in files]
        if all(now.get("sha256") == item["after_sha256"] for now, item in zip(current, files)):
            if receipt.exists() and json.loads(receipt.read_text()).get("result") == "APPLIED":
                return {"result": "ALREADY_APPLIED", "reload_performed": False}
            raise ValueError("candidate files exist without completed receipt; inspect before recovery")
        if any(now != item["before"] for now, item in zip(current, files)):
            raise ValueError("configuration changed since plan; no files written")
        pid_before = master_pid(master_pid_file)
        changed = []
        reload_attempted = False
        try:
            # Install the format first: old locations do not reference it yet.
            for item in reversed(files):
                path = Path(item["path"])
                if snapshot(path) != item["before"]:
                    raise ValueError("configuration CAS failed immediately before write")
                metadata = item["before"] if item["before"]["exists"] else {
                    "mode": 0o644, "uid": os.geteuid(), "gid": os.getegid()}
                atomic_write(path, (directory / item["after_file"]).read_bytes(), metadata)
                changed.append(item)
            check = subprocess.run(command + ["-t"], capture_output=True, timeout=30)
            if check.returncode:
                raise RuntimeError("candidate nginx syntax check failed")
            if any(snapshot(Path(item["path"])).get("sha256") != item["after_sha256"] for item in files):
                raise RuntimeError("configuration changed before reload")
            reload_attempted = True
            reload = subprocess.run(command + ["-s", "reload"], capture_output=True, timeout=30)
            if reload.returncode:
                raise RuntimeError("nginx reload command failed")
            pid_after = master_pid(master_pid_file)
            if pid_after != pid_before:
                raise RuntimeError("nginx master PID changed unexpectedly")
            result = {"result": "APPLIED", "syntax_check_passed": True,
                      "reload_performed": True, "nginx_restarted": False,
                      "master_pid_before": pid_before, "master_pid_after": pid_after,
                      "master_pid_checked": master_pid_file is not None,
                      "candidate_sha256": [item["after_sha256"] for item in files]}
        except Exception as error:
            for item in reversed(changed):
                path = Path(item["path"])
                if snapshot(path).get("sha256") != item["after_sha256"]:
                    raise RuntimeError("rollback refused: external configuration change; manual recovery required") from error
                if item["before"]["exists"]:
                    atomic_write(path, (directory / item["before_file"]).read_bytes(), item["before"])
                else:
                    path.unlink()
            rollback_check = subprocess.run(command + ["-t"], capture_output=True, timeout=30)
            rollback_reload_ok = None
            if reload_attempted and rollback_check.returncode == 0:
                restored = subprocess.run(command + ["-s", "reload"], capture_output=True, timeout=30)
                rollback_reload_ok = restored.returncode == 0
            result = {"result": "ROLLED_BACK", "syntax_check_passed": False,
                      "baseline_syntax_check_passed": rollback_check.returncode == 0,
                      "reload_attempted": reload_attempted,
                      "rollback_reload_passed": rollback_reload_ok,
                      "failure": type(error).__name__}
            receipt.write_text(json.dumps(result, indent=2) + "\n")
            receipt.chmod(0o600)
            raise RuntimeError("nginx candidate failed; inspect rollback receipt") from error
        receipt.write_text(json.dumps(result, indent=2) + "\n")
        receipt.chmod(0o600)
        return result


def rollback_plan(directory: Path, lock: Path, nginx: str,
                  config: str | None = None, prefix: str | None = None,
                  master_pid_file: Path | None = None) -> dict:
    plan = json.loads((directory / "plan.json").read_text())
    if plan.get("schema") != 1 or len(plan.get("files", [])) != 2:
        raise ValueError("unsupported plan")
    files = plan["files"]
    for item in files:
        if digest((directory / item["after_file"]).read_bytes()) != item["after_sha256"]:
            raise ValueError("candidate backup changed after review")
        if item["before"]["exists"]:
            if digest((directory / item["before_file"]).read_bytes()) != item["before"]["sha256"]:
                raise ValueError("original backup changed after review")
    command = [nginx]
    if prefix:
        command += ["-p", prefix]
    if config:
        command += ["-c", config]
    receipt = directory / "rollback-result.json"
    with lock.open("a") as lock_file:
        fcntl.flock(lock_file, fcntl.LOCK_EX | fcntl.LOCK_NB)
        current = [snapshot(Path(item["path"])) for item in files]
        if all(now == item["before"] for now, item in zip(current, files)):
            if receipt.exists() and json.loads(receipt.read_text()).get("result") == "RESTORED":
                return {"result": "ALREADY_RESTORED", "reload_performed": False}
            raise ValueError("baseline files exist without completed rollback receipt; inspect before recovery")
        if any(now.get("sha256") != item["after_sha256"] for now, item in zip(current, files)):
            raise ValueError("rollback CAS refused: configuration changed after apply")
        pid_before = master_pid(master_pid_file)
        restored = []
        reload_attempted = False
        try:
            # First remove candidate location references, then remove its format.
            for item in files:
                path = Path(item["path"])
                if snapshot(path).get("sha256") != item["after_sha256"]:
                    raise ValueError("rollback CAS failed immediately before write")
                if item["before"]["exists"]:
                    atomic_write(path, (directory / item["before_file"]).read_bytes(), item["before"])
                else:
                    path.unlink()
                restored.append(item)
            check = subprocess.run(command + ["-t"], capture_output=True, timeout=30)
            if check.returncode:
                raise RuntimeError("restored nginx baseline syntax check failed")
            if any(snapshot(Path(item["path"])) != item["before"] for item in files):
                raise RuntimeError("restored configuration changed before reload")
            reload_attempted = True
            reload = subprocess.run(command + ["-s", "reload"], capture_output=True, timeout=30)
            if reload.returncode:
                raise RuntimeError("restored nginx reload command failed")
            pid_after = master_pid(master_pid_file)
            if pid_after != pid_before:
                raise RuntimeError("nginx master PID changed unexpectedly during rollback")
            result = {"result": "RESTORED", "baseline_syntax_check_passed": True,
                      "reload_performed": True, "nginx_restarted": False,
                      "master_pid_before": pid_before, "master_pid_after": pid_after,
                      "master_pid_checked": master_pid_file is not None}
        except Exception as error:
            # Recovery itself is CAS-protected; never overwrite another writer.
            for item in reversed(restored):
                path = Path(item["path"])
                if snapshot(path) != item["before"]:
                    raise RuntimeError("rollback recovery refused: external configuration change") from error
                metadata = item["before"] if item["before"]["exists"] else {
                    "mode": 0o644, "uid": os.geteuid(), "gid": os.getegid()}
                atomic_write(path, (directory / item["after_file"]).read_bytes(), metadata)
            candidate_check = subprocess.run(command + ["-t"], capture_output=True, timeout=30)
            recovery_reload_ok = None
            if reload_attempted and candidate_check.returncode == 0:
                recover = subprocess.run(command + ["-s", "reload"], capture_output=True, timeout=30)
                recovery_reload_ok = recover.returncode == 0
            result = {"result": "ROLLBACK_FAILED_CANDIDATE_RESTORED",
                      "candidate_syntax_check_passed": candidate_check.returncode == 0,
                      "reload_attempted": reload_attempted,
                      "candidate_reload_passed": recovery_reload_ok,
                      "failure": type(error).__name__}
            receipt.write_text(json.dumps(result, indent=2) + "\n")
            receipt.chmod(0o600)
            raise RuntimeError("nginx rollback failed; inspect recovery receipt") from error
        receipt.write_text(json.dumps(result, indent=2) + "\n")
        receipt.chmod(0o600)
        return result


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    plan = sub.add_parser("plan")
    plan.add_argument("--snippet", type=Path, required=True)
    plan.add_argument("--http-conf", type=Path, required=True)
    plan.add_argument("--output", type=Path, required=True)
    plan.add_argument("--expected-sha256", required=True)
    for operation in ["apply", "rollback"]:
        action = sub.add_parser(operation)
        action.add_argument("--plan", type=Path, required=True)
        action.add_argument("--lock", type=Path, required=True)
        action.add_argument("--nginx", default="nginx")
        action.add_argument("--config")
        action.add_argument("--prefix")
        action.add_argument("--master-pid-file", type=Path, required=True)
        action.add_argument("--reload", action="store_true", required=True,
                            help="explicitly authorize syntax-checked graceful reload")
    args = parser.parse_args()
    if args.command == "plan":
        result = make_plan(args.snippet, args.http_conf, args.output, args.expected_sha256)
        print(json.dumps({"result": "PLANNED", "production_changed": False,
                          "after_sha256": [item["after_sha256"] for item in result["files"]]}))
    else:
        operation = apply_plan if args.command == "apply" else rollback_plan
        print(json.dumps(operation(args.plan, args.lock, args.nginx, args.config,
                                   args.prefix, args.master_pid_file)))


if __name__ == "__main__":
    main()
