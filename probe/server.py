"""Throwaway probe: runs a shell command on the Cloud Run host container.

Deployed with --no-allow-unauthenticated, so only IAM-authorised callers can
reach it. Used once to measure Cloud Run sandbox behaviour, then deleted.
"""
import json
import subprocess
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


class H(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"ok\n")

    def do_PUT(self):
        n = int(self.headers.get("content-length", "0"))
        path = self.headers.get("x-path")
        with open(path, "wb") as f:
            f.write(self.rfile.read(n))
        import os
        os.chmod(path, int(self.headers.get("x-mode", "644"), 8))
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"saved\n")

    def do_POST(self):
        n = int(self.headers.get("content-length", "0"))
        cmd = self.rfile.read(n).decode()
        timeout = float(self.headers.get("x-timeout", "60"))
        t0 = time.perf_counter()
        try:
            p = subprocess.run(["bash", "-c", cmd], capture_output=True, timeout=timeout)
            out = {"code": p.returncode, "out": p.stdout.decode(errors="replace"),
                   "err": p.stderr.decode(errors="replace")}
        except subprocess.TimeoutExpired as e:
            out = {"code": -1, "out": (e.stdout or b"").decode(errors="replace"),
                   "err": "TIMEOUT " + (e.stderr or b"").decode(errors="replace")}
        out["ms"] = round((time.perf_counter() - t0) * 1000, 1)
        body = json.dumps(out).encode()
        self.send_response(200)
        self.send_header("content-type", "application/json")
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *a):
        pass


ThreadingHTTPServer(("0.0.0.0", 8080), H).serve_forever()
