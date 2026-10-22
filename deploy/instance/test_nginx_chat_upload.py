#!/usr/bin/env python3
"""Free nginx upload/rollback regression; integration binds loopback only.

python3 test_nginx_chat_upload.py
python3 test_nginx_chat_upload.py --integration --baseline FILE --output NEWDIR
The integration performs one 31s body pause and one 31s stub response. It never
contacts production, providers or SMS and does not replay model requests.
"""
from __future__ import annotations

import argparse
from datetime import datetime, timezone
import http.server
import json
from pathlib import Path
import re
import shutil
import signal
import socket
import subprocess
import tempfile
import threading
import time
import unittest
from unittest.mock import patch

import nginx_chat_upload as subject


FIXTURE = (subject.RELAY + '''
    limit_req zone=yunshu_harness burst=100 nodelay;
    limit_req_status 429;
    client_max_body_size 100m;
    rewrite ^/harness/(.*)$ /$1 break;
    proxy_pass http://127.0.0.1:3010;
    proxy_connect_timeout 5s;
    proxy_send_timeout 3600s;
    proxy_read_timeout 3600s;
    proxy_buffering off;
}
location /unrelated/ { return 204; }
''').encode()


class InstallerTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.base = Path(self.directory.name)
        self.snippet = self.base / "harness.conf"
        self.snippet.write_bytes(FIXTURE)
        self.snippet.chmod(0o640)
        self.metrics = self.base / "metrics.conf"
        self.plan = self.base / "plan"
        self.lock = self.base / "lock"
        subject.make_plan(self.snippet, self.metrics, self.plan, subject.digest(FIXTURE))

    def test_only_new_chat_block_added_and_safeguards_kept(self):
        after = (self.plan / "snippet.after.conf").read_bytes()
        begin = after.index(subject.MARKER.encode())
        end = after.index(b"# END managed harness chat upload v1\n\n")
        end += len(b"# END managed harness chat upload v1\n\n")
        self.assertEqual(after[:begin] + after[end:], FIXTURE)
        block = after[begin:end].decode()
        self.assertIn("location ~ ^/harness/v1/chat/completions/?$ {", block)
        for directive in ["client_body_timeout 120s;", "proxy_request_buffering on;",
                          "proxy_next_upstream off;", "client_max_body_size 100m;",
                          "limit_req_status 429;", "proxy_read_timeout 3600s;"]:
            self.assertIn(directive, block)

    def test_cas_refuses_external_edit_without_write_or_reload(self):
        self.snippet.write_bytes(FIXTURE + b"# concurrent edit\n")
        with patch.object(subject.subprocess, "run") as run:
            with self.assertRaisesRegex(ValueError, "configuration changed"):
                subject.apply_plan(self.plan, self.lock, "nginx")
        run.assert_not_called()
        self.assertFalse(self.metrics.exists())
        self.assertTrue(self.snippet.read_bytes().endswith(b"# concurrent edit\n"))

    def test_invalid_candidate_restores_bytes_modes_and_no_reload(self):
        checks = [subprocess.CompletedProcess([], 1), subprocess.CompletedProcess([], 0)]
        with patch.object(subject.subprocess, "run", side_effect=checks) as run:
            with self.assertRaisesRegex(RuntimeError, "inspect rollback receipt"):
                subject.apply_plan(self.plan, self.lock, "nginx")
        self.assertEqual(self.snippet.read_bytes(), FIXTURE)
        self.assertEqual(self.snippet.stat().st_mode & 0o777, 0o640)
        self.assertFalse(self.metrics.exists())
        self.assertTrue(all(call.args[0][-1] == "-t" for call in run.call_args_list))
        self.assertEqual(json.loads((self.plan / "apply-result.json").read_text())["result"], "ROLLED_BACK")

    def test_success_is_idempotent_and_does_not_reload_twice(self):
        with patch.object(subject.subprocess, "run", return_value=subprocess.CompletedProcess([], 0)) as run:
            result = subject.apply_plan(self.plan, self.lock, "nginx")
            second = subject.apply_plan(self.plan, self.lock, "nginx")
        self.assertEqual(result["result"], "APPLIED")
        self.assertEqual(second["result"], "ALREADY_APPLIED")
        self.assertEqual(run.call_count, 2)

    def test_tampered_candidate_refused_before_write(self):
        with (self.plan / "snippet.after.conf").open("ab") as file:
            file.write(b"# tamper\n")
        with patch.object(subject.subprocess, "run") as run:
            with self.assertRaisesRegex(ValueError, "candidate content changed"):
                subject.apply_plan(self.plan, self.lock, "nginx")
        run.assert_not_called()
        self.assertEqual(self.snippet.read_bytes(), FIXTURE)

    def test_reload_failure_restores_and_reload_original(self):
        responses = [subprocess.CompletedProcess([], code) for code in [0, 1, 0, 0]]
        with patch.object(subject.subprocess, "run", side_effect=responses) as run:
            with self.assertRaisesRegex(RuntimeError, "inspect rollback receipt"):
                subject.apply_plan(self.plan, self.lock, "nginx")
        self.assertEqual(self.snippet.read_bytes(), FIXTURE)
        self.assertFalse(self.metrics.exists())
        self.assertEqual(run.call_count, 4)
        receipt = json.loads((self.plan / "apply-result.json").read_text())
        self.assertTrue(receipt["rollback_reload_passed"])

    def test_log_schema_never_uses_sensitive_fields(self):
        metrics = subject.METRICS
        for field in ["$remote_addr", "$request_uri", "$uri", "$request_body",
                      "$http_authorization", "$http_cookie", "$http_user_agent"]:
            self.assertNotIn(field, metrics)
        self.assertIn('"~^[0-9]{1,19}$"', metrics)
        self.assertIn('"upstream_response_time":"$upstream_response_time"', metrics)

    def test_explicit_rollback_restores_original_and_is_idempotent(self):
        with patch.object(subject.subprocess, "run", return_value=subprocess.CompletedProcess([], 0)) as run:
            subject.apply_plan(self.plan, self.lock, "nginx")
            result = subject.rollback_plan(self.plan, self.lock, "nginx")
            again = subject.rollback_plan(self.plan, self.lock, "nginx")
        self.assertEqual(result["result"], "RESTORED")
        self.assertEqual(again["result"], "ALREADY_RESTORED")
        self.assertEqual(run.call_count, 4)
        self.assertEqual(self.snippet.read_bytes(), FIXTURE)
        self.assertEqual(self.snippet.stat().st_mode & 0o777, 0o640)
        self.assertFalse(self.metrics.exists())

    def test_explicit_rollback_refuses_concurrent_edit(self):
        with patch.object(subject.subprocess, "run", return_value=subprocess.CompletedProcess([], 0)):
            subject.apply_plan(self.plan, self.lock, "nginx")
        changed = self.snippet.read_bytes() + b"# concurrent change\n"
        self.snippet.write_bytes(changed)
        with patch.object(subject.subprocess, "run") as run:
            with self.assertRaisesRegex(ValueError, "rollback CAS refused"):
                subject.rollback_plan(self.plan, self.lock, "nginx")
        run.assert_not_called()
        self.assertEqual(self.snippet.read_bytes(), changed)
        self.assertTrue(self.metrics.exists())

    def test_explicit_rollback_syntax_failure_recovers_candidate(self):
        with patch.object(subject.subprocess, "run", return_value=subprocess.CompletedProcess([], 0)):
            subject.apply_plan(self.plan, self.lock, "nginx")
        candidate = self.snippet.read_bytes()
        with patch.object(subject.subprocess, "run", side_effect=[subprocess.CompletedProcess([], 1), subprocess.CompletedProcess([], 0)]) as run:
            with self.assertRaisesRegex(RuntimeError, "inspect recovery receipt"):
                subject.rollback_plan(self.plan, self.lock, "nginx")
        self.assertEqual(self.snippet.read_bytes(), candidate)
        self.assertTrue(self.metrics.exists())
        self.assertTrue(all(call.args[0][-1] == "-t" for call in run.call_args_list))

    def test_master_pid_is_checked_without_restart(self):
        import os
        pid_file = self.base / "nginx.pid"
        pid_file.write_text(str(os.getpid()))
        with patch.object(subject.subprocess, "run", return_value=subprocess.CompletedProcess([], 0)):
            result = subject.apply_plan(self.plan, self.lock, "nginx", master_pid_file=pid_file)
            restored = subject.rollback_plan(self.plan, self.lock, "nginx", master_pid_file=pid_file)
        for receipt in [result, restored]:
            self.assertTrue(receipt["master_pid_checked"])
            self.assertEqual(receipt["master_pid_before"], receipt["master_pid_after"])
            self.assertFalse(receipt["nginx_restarted"])


def integration(baseline: Path, output: Path, nginx: str) -> dict:
    output.mkdir(parents=True, mode=0o700, exist_ok=False)
    requests = []
    lock = threading.Lock()

    class Stub(http.server.BaseHTTPRequestHandler):
        def do_POST(self):
            content = self.rfile.read(int(self.headers.get("Content-Length", "0")))
            with lock:
                requests.append({"body_bytes": len(content), "method": "POST"})
            if "slow=1" in self.path:
                time.sleep(31)
            self.send_response(204)
            self.send_header("Content-Length", "0")
            self.end_headers()

        def do_GET(self):
            self.send_response(204)
            self.send_header("Content-Length", "0")
            self.end_headers()

        def log_message(self, *args):
            pass

    stub = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Stub)
    stub.daemon_threads = True
    thread = threading.Thread(target=stub.serve_forever, daemon=True)
    thread.start()
    with socket.socket() as reserve:
        reserve.bind(("127.0.0.1", 0))
        port = reserve.getsockname()[1]
    original = baseline.read_bytes()
    candidate = subject.candidate_snippet(original).decode()
    # Only local fixture endpoints/logs differ from the exact production candidate.
    local = candidate.replace("http://127.0.0.1:3010", f"http://127.0.0.1:{stub.server_address[1]}")
    local = local.replace("/var/log/nginx/", str(output) + "/")
    (output / "harness.conf").write_text(local)
    (output / "metrics.conf").write_text(subject.METRICS)
    config = f'''worker_processes 1;
pid {output}/nginx.pid;
error_log {output}/fixture-error.log crit;
events {{ worker_connections 64; }}
http {{
    limit_req_zone $binary_remote_addr zone=yunshu_harness:1m rate=100r/s;
    log_format yunshu_harness_safe '$status';
    include {output}/metrics.conf;
    access_log off;
    server {{
        listen 127.0.0.1:{port};
        client_body_timeout 30s;
        include {output}/harness.conf;
        location /other-business/ {{ return 204; }}
    }}
}}
'''
    (output / "nginx.conf").write_text(config)
    version = subprocess.run([nginx, "-v"], capture_output=True, text=True, check=True)
    report = {"schema": 1, "started_at_utc": datetime.now(timezone.utc).isoformat(),
              "nginx_version": (version.stdout + version.stderr).strip(),
              "production_nginx_version": "nginx/1.18.0", "provider_calls": 0,
              "sms_calls": 0, "production_contacted": False, "cases": [],
              "baseline_sha256": subject.digest(original),
              "candidate_sha256": subject.digest(candidate.encode()),
              "metrics_sha256": subject.digest(subject.METRICS.encode())}
    process = None

    def metrics():
        path = output / "yunshu-harness-chat-metrics.log"
        return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []

    def request(name, path, pause=0, extra_headers=b"", complete=True, expected=204):
        before = len(requests)
        started = time.monotonic()
        with socket.create_connection(("127.0.0.1", port), timeout=2) as connection:
            connection.settimeout(40)
            message = (f"POST {path} HTTP/1.1\r\nHost: fixture.invalid\r\n"
                       "Content-Type: application/json\r\nContent-Length: 2\r\nConnection: close\r\n").encode()
            connection.sendall(message + extra_headers + b"\r\n{")
            if pause:
                time.sleep(pause)
            if complete:
                connection.sendall(b"}")
            response = b""
            while b"\r\n\r\n" not in response:
                part = connection.recv(4096)
                if not part:
                    break
                response += part
        status = re.search(rb"^HTTP/1\.[01] ([0-9]{3})", response)
        got = int(status[1]) if status else None
        time.sleep(.05)
        case = {"name": name, "http_status_received": got,
                "elapsed_seconds": time.monotonic() - started,
                "stub_requests": len(requests) - before, "pause_seconds": pause}
        report["cases"].append(case)
        if expected == 408:
            assert metrics()[-1]["status"] == 408 and case["stub_requests"] == 0, case
        else:
            assert got == expected and case["stub_requests"] == 1, case
        return case

    try:
        check = subprocess.run([nginx, "-p", str(output) + "/", "-c", str(output / "nginx.conf"), "-t"],
                               capture_output=True, timeout=10)
        (output / "syntax-check.log").write_bytes(check.stdout + check.stderr)
        assert check.returncode == 0, "local candidate configuration invalid"
        with (output / "process.log").open("wb") as process_log:
            process = subprocess.Popen([nginx, "-p", str(output) + "/", "-c", str(output / "nginx.conf"),
                                        "-g", "daemon off;"], stdout=process_log, stderr=process_log)
        deadline = time.monotonic() + 5
        while True:
            assert process.poll() is None, "fixture nginx exited"
            try:
                with socket.create_connection(("127.0.0.1", port), timeout=.2):
                    break
            except OSError:
                assert time.monotonic() < deadline, "fixture nginx readiness timeout"
                time.sleep(.05)
        request("normal_body", "/harness/v1/chat/completions")
        request("old_threshold_exceeded_31s_body_pause", "/harness/v1/chat/completions", pause=31)
        request("trailing_slash_compatibility", "/harness/v1/chat/completions/")
        request("upstream_response_slow_31s", "/harness/v1/chat/completions?slow=1")
        sensitive = b"Authorization: Bearer SYNTHETIC_SECRET_CANARY\r\nCookie: synthetic=PII_CANARY\r\nUser-Agent: UA_CANARY\r\n"
        request("private_header_query_body_canaries_not_logged", "/harness/v1/chat/completions?PII_QUERY_CANARY=1", extra_headers=sensitive)
        rows_before = len(metrics())
        request("other_relay_path_unchanged", "/harness/v1/responses")
        request("chat_child_path_keeps_original_relay", "/harness/v1/chat/completions/extra")
        request("console_api_unchanged", "/harness/api/status")
        assert len(metrics()) == rows_before, "chat metrics leaked into sibling paths"
        # Timeout negative control shortens ONLY the local chat threshold from120s
        # to2s. This proves finite inactivity semantics, not a real120s wait.
        short = local.replace("client_body_timeout 120s;", "client_body_timeout 2s;")
        (output / "harness.conf").write_text(short)
        control = subprocess.run([nginx, "-p", str(output) + "/", "-c", str(output / "nginx.conf"), "-s", "reload"],
                                 capture_output=True, timeout=10)
        assert control.returncode == 0
        time.sleep(.3)
        request("shortened_2s_finite_timeout_control", "/harness/v1/chat/completions", complete=False, expected=408)
        raw_metrics = (output / "yunshu-harness-chat-metrics.log").read_text()
        for canary in ["SYNTHETIC_SECRET_CANARY", "PII_CANARY", "UA_CANARY", "PII_QUERY_CANARY", "127.0.0.1", "fixture.invalid", "chat/completions"]:
            assert canary not in raw_metrics, "sensitive or raw route field found in metrics"
        for row in metrics():
            assert re.fullmatch(r"[0-9a-f]{32}", row["request_id"])
            assert re.fullmatch(r"[0-9]{0,19}", row["content_length"])
            assert isinstance(row["upstream_response_time"], str)
            assert isinstance(row["status"], int)
        report["metrics_records"] = metrics()
        report["result"] = "PASS"
        report["limitations"] = ["Local nginx1.29.8 differs from production1.18.0; production nginx-t still required before reload.",
                                  "2s negative control proves bounded inactivity mechanism; no claim of actual120s stall test.",
                                  "Historical three408 exact request root causes remain inference; no raw historical body/length capture."]
    except Exception as error:
        report["result"] = "FAIL"
        report["failure"] = type(error).__name__ + ": " + str(error)
        raise
    finally:
        if process is not None:
            if process.poll() is None:
                process.send_signal(signal.SIGQUIT)
                process.wait(timeout=10)
            report["owned_nginx_exit_code"] = process.returncode
        stub.shutdown()
        stub.server_close()
        thread.join(timeout=2)
        report["stub_stopped"] = not thread.is_alive()
        for name, checked_port in [("nginx", port), ("stub", stub.server_address[1])]:
            with socket.socket() as probe:
                probe.settimeout(.2)
                report[name + "_port_closed"] = probe.connect_ex(("127.0.0.1", checked_port)) != 0
        report["synthetic_stub_request_count"] = len(requests)
        report["finished_at_utc"] = datetime.now(timezone.utc).isoformat()
        (output / "result.json").write_text(json.dumps(report, indent=2) + "\n")
    return report


if __name__ == "__main__":
    if "--integration" in __import__("sys").argv:
        parser = argparse.ArgumentParser(description=__doc__)
        parser.add_argument("--integration", action="store_true")
        parser.add_argument("--baseline", type=Path, required=True)
        parser.add_argument("--output", type=Path, required=True)
        parser.add_argument("--nginx", default=shutil.which("nginx"))
        args = parser.parse_args()
        result = integration(args.baseline, args.output, args.nginx)
        print(json.dumps({"result": result["result"], "cases": result["cases"],
                          "evidence": str(args.output / "result.json")}))
    else:
        unittest.main()
