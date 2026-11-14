#!/usr/bin/env python3
"""Spike test: submit N different programs at once and wait for all of them.

    JUDGE_URL=https://... JUDGE_KEY=... python3 tests/burst.py [n] [language]

Every program is different, so nothing is grouped: N submissions mean N
sandboxes. Reports how long the whole burst took and whether every verdict
is right. Languages: python (default), cpp, java.
"""
import json
import os
import subprocess
import sys
import threading
import time

URL = os.environ.get("JUDGE_URL", "").rstrip("/")
KEY = os.environ.get("JUDGE_KEY", "")
if not URL or not KEY:
    sys.exit("set JUDGE_URL and JUDGE_KEY")

N = int(sys.argv[1]) if len(sys.argv) > 1 else 200
LANG = sys.argv[2] if len(sys.argv) > 2 else "python"

PROGRAMS = {
    "python": (71, "import sys\nn = int(sys.stdin.readline())\nprint(sum(i * {k} for i in range(n)))"),
    "cpp": (54, "#include <bits/stdc++.h>\nint main(){{ long long n, s = 0; std::cin >> n; for (long long i = 0; i < n; i++) s += i * {k}; std::cout << s << \"\\n\"; }}"),
    "java": (62, "import java.util.*;\npublic class Main {{ public static void main(String[] a) {{ long n = new Scanner(System.in).nextLong(), s = 0; for (long i = 0; i < n; i++) s += i * {k}; System.out.println(s); }} }}"),
}
lang_id, template = PROGRAMS[LANG]
INPUT_N = 1000


def http(method, path, body=None):
    cmd = ["curl", "-sS", "-X", method, "-H", "X-Auth-Token: " + KEY, URL + path]
    if body is not None:
        cmd += ["-H", "Content-Type: application/json", "--data-binary", "@-"]
    p = subprocess.run(cmd, input=json.dumps(body).encode() if body is not None else None, capture_output=True, timeout=120)
    try:
        return json.loads(p.stdout)
    except ValueError:
        return None


subs = []
for k in range(1, N + 1):
    expected = sum(i * k for i in range(INPUT_N))
    subs.append({"language_id": lang_id, "source_code": template.format(k=k), "stdin": str(INPUT_N), "expected_output": str(expected)})

tokens = []
lock = threading.Lock()


def submit(chunk):
    r = http("POST", "/submissions/batch", {"submissions": chunk}) or []
    with lock:
        tokens.extend(x.get("token") for x in r if isinstance(x, dict) and x.get("token"))


t0 = time.time()
threads = [threading.Thread(target=submit, args=(subs[i:i + 100],)) for i in range(0, N, 100)]
[t.start() for t in threads]
[t.join() for t in threads]
t_submitted = time.time() - t0
print(f"submitted {len(tokens)}/{N} {LANG} programs in {t_submitted:.1f}s")

status, last = {}, 0
while time.time() - t0 < 600:
    pending = [t for t in tokens if status.get(t, 1) <= 2]
    if not pending:
        break
    for i in range(0, len(pending), 100):
        r = http("GET", "/submissions/batch?fields=token,status&tokens=" + ",".join(pending[i:i + 100])) or {}
        for s in r.get("submissions") or []:
            if s:
                status[s["token"]] = s["status"]["id"]
    done = sum(1 for v in status.values() if v > 2)
    if done != last:
        print(f"  {time.time() - t0:5.1f}s  {done}/{len(tokens)} finished")
        last = done
    time.sleep(1)

elapsed = time.time() - t0
counts = {}
for t in tokens:
    counts[status.get(t, 1)] = counts.get(status.get(t, 1), 0) + 1
names = {1: "still queued", 2: "processing", 3: "Accepted", 4: "Wrong Answer", 5: "TLE", 6: "Compile Error", 13: "Internal Error"}
print(f"\nburst of {len(tokens)} finished in {elapsed:.1f}s")
for k, v in sorted(counts.items()):
    print(f"  {names.get(k, 'status ' + str(k))}: {v}")
ok = len(tokens) == N and counts.get(3, 0) == N
print("RESULT:", "all accepted" if ok else "NOT all accepted")
sys.exit(0 if ok else 1)
