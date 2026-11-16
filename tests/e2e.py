#!/usr/bin/env python3
"""End-to-end checks against a deployed judge.

    JUDGE_URL=https://... JUDGE_KEY=... python3 tests/e2e.py [filter ...]

Every case states the verdict it expects, so a wrong "Accepted" fails just
like a wrong "Time Limit Exceeded". Filters match case names, for example
`python3 tests/e2e.py cpp sql`. Uses curl for HTTP, so it needs no packages.
"""
import base64
import json
import os
import subprocess
import sys
import time

URL = os.environ.get("JUDGE_URL", "").rstrip("/")
KEY = os.environ.get("JUDGE_KEY", "")
if not URL or not KEY:
    sys.exit("set JUDGE_URL and JUDGE_KEY")

ACCEPTED, WRONG, TLE, COMPILE, SEGV, XFSZ, NZEC, MLE = 3, 4, 5, 6, 7, 8, 11, 15


def http(method, path, body=None):
    """Returns (status code, parsed JSON or None, Server-Timing dict)."""
    cmd = ["curl", "-sS", "-X", method, "-D", "-", "-H", "X-Auth-Token: " + KEY, URL + path]
    if body is not None:
        cmd += ["-H", "Content-Type: application/json", "--data-binary", "@-"]
    data = json.dumps(body).encode() if body is not None else None
    for attempt in range(3):
        p = subprocess.run(cmd, input=data, capture_output=True, timeout=180)
        if p.stdout.startswith(b"HTTP/"):
            break
        time.sleep(2)  # no response at all: the connection failed, not the judge
    raw = p.stdout.decode(errors="replace")
    head, _, payload = raw.partition("\r\n\r\n")
    while payload.startswith("HTTP/"):  # skip interim responses
        head, _, payload = payload.partition("\r\n\r\n")
    code = int(head.split()[1]) if head.startswith("HTTP/") else 0
    timing = {}
    for line in head.split("\r\n"):
        if line.lower().startswith("server-timing:"):
            for part in line.split(":", 1)[1].split(","):
                name, _, rest = part.strip().partition(";")
                timing[name] = rest.split("=", 1)[-1]
    try:
        return code, json.loads(payload), timing
    except ValueError:
        return code, None, timing


SUM = {
    "c": (50, '#include <stdio.h>\nint main(){long a,b; scanf("%ld %ld",&a,&b); printf("%ld\\n",a+b); return 0;}'),
    "cpp": (54, '#include <bits/stdc++.h>\nusing namespace std;\nint main(){long long a,b; cin>>a>>b; vector<long long> v{a,b}; cout<<accumulate(v.begin(),v.end(),0LL)<<"\\n";}'),
    "python": (71, "a, b = map(int, input().split())\nprint(a + b)"),
    "javascript": (63, 'const [a, b] = require("fs").readFileSync(0, "utf8").trim().split(/\\s+/).map(Number);\nconsole.log(a + b);'),
    "typescript": (74, 'import * as fs from "fs";\nconst nums: number[] = fs.readFileSync(0, "utf8").trim().split(/\\s+/).map(Number);\nconst total: number = nums.reduce((x, y) => x + y, 0);\nconsole.log(total);'),
    "java": (62, "import java.util.*;\npublic class Main { public static void main(String[] x) { Scanner s = new Scanner(System.in); long a = s.nextLong(), b = s.nextLong(); System.out.println(a + b); } }"),
    "csharp": (51, 'using System;\nusing System.Linq;\nclass P { static void Main() { var n = Console.ReadLine().Split(\' \').Select(long.Parse).ToArray(); Console.WriteLine(n[0] + n[1]); } }'),
    "go": (60, 'package main\nimport "fmt"\nfunc main() { var a, b int64; fmt.Scan(&a, &b); fmt.Println(a + b) }'),
    "php": (68, '<?php\nfscanf(STDIN, "%d %d", $a, $b);\necho $a + $b, "\\n";'),
    "rust": (73, 'use std::io::*;\nfn main() { let mut s = String::new(); stdin().read_to_string(&mut s).unwrap(); let v: Vec<i64> = s.split_whitespace().map(|x| x.parse().unwrap()).collect(); println!("{}", v[0] + v[1]); }'),
    # The languages that share a service with others (script, jvm, swift,
    # functional, native).
    "ruby": (72, "a, b = gets.split.map(&:to_i)\nputs a + b"),
    "perl": (85, 'my ($a, $b) = split " ", <STDIN>;\nprint $a + $b, "\\n";'),
    "lua": (64, 'local a, b = io.read("n", "n")\nprint(a + b)'),
    "bash": (46, "read a b\necho $((a + b))"),
    "r": (80, 'x <- scan(file("stdin"), quiet = TRUE)\ncat(sum(x), "\\n", sep = "")'),
    "kotlin": (78, 'fun main() {\n    val (a, b) = readLine()!!.split(" ").map { it.toLong() }\n    println(a + b)\n}'),
    "scala": (81, 'object Main {\n  def main(args: Array[String]): Unit = {\n    val Array(a, b) = scala.io.StdIn.readLine().split(" ").map(_.toLong)\n    println(a + b)\n  }\n}'),
    "groovy": (88, 'def (a, b) = System.in.newReader().readLine().split(" ")*.toLong()\nprintln(a + b)'),
    "clojure": (86, "(require '[clojure.string :as str])\n(let [[a b] (map #(Long/parseLong %) (str/split (read-line) #\" \"))]\n  (println (+ a b)))"),
    "swift": (83, 'let p = readLine()!.split(separator: " ").map { Int($0)! }\nprint(p[0] + p[1])'),
    "haskell": (61, "main :: IO ()\nmain = do\n  [a, b] <- map read . words <$> getLine :: IO [Integer]\n  print (a + b)"),
    "ocaml": (65, 'let () = Scanf.scanf " %d %d" (fun a b -> Printf.printf "%d\\n" (a + b))'),
    "elixir": (57, "[a, b] = IO.read(:stdio, :line) |> String.split() |> Enum.map(&String.to_integer/1)\nIO.puts(a + b)"),
    "erlang": (58, 'main(_) ->\n    {ok, [A, B]} = io:fread("", "~d ~d"),\n    io:format("~p~n", [A + B]).'),
    "dart": (90, "import 'dart:io';\nvoid main() {\n  final p = stdin.readLineSync()!.split(' ').map(int.parse).toList();\n  print(p[0] + p[1]);\n}"),
    "fortran": (59, 'program s\n  integer(8) :: a, b\n  read(*,*) a, b\n  print "(I0)", a + b\nend program s'),
    "pascal": (67, "var a, b: int64;\nbegin\n  readln(a, b);\n  writeln(a + b);\nend."),
    "objective-c": (79, '#import <Foundation/Foundation.h>\nint main(void) {\n  @autoreleasepool {\n    long a, b;\n    scanf("%ld %ld", &a, &b);\n    NSNumber *sum = @(a + b);\n    printf("%s\\n", [[sum stringValue] UTF8String]);\n  }\n  return 0;\n}'),
    "d": (56, 'import std.stdio;\nvoid main() { long a, b; readf(" %d %d", &a, &b); writeln(a + b); }'),
    "cobol": (77, "IDENTIFICATION DIVISION.\nPROGRAM-ID. S.\nDATA DIVISION.\nWORKING-STORAGE SECTION.\n01 L PIC X(40).\n01 A PIC 9(9).\n01 B PIC 9(9).\n01 R PIC Z(9)9.\nPROCEDURE DIVISION.\n    ACCEPT L.\n    UNSTRING L DELIMITED BY SPACE INTO A B.\n    COMPUTE R = A + B.\n    DISPLAY FUNCTION TRIM(R).\n    STOP RUN."),
    "c-clang": (75, '#include <stdio.h>\nint main(){long a,b; scanf("%ld %ld",&a,&b); printf("%ld\\n",a+b); return 0;}'),
    "cpp-clang": (76, '#include <bits/stdc++.h>\nusing namespace std;\nint main(){long long a,b; cin>>a>>b; cout<<a+b<<"\\n";}'),
}

FIXTURE = "CREATE TABLE emp (id INT PRIMARY KEY, name VARCHAR(20), dept VARCHAR(20), salary INT);\n" \
          "INSERT INTO emp VALUES (1,'ann','eng',120),(2,'bob','eng',100),(3,'cy','ops',90),(4,'di','ops',NULL);\n"
QUERY = "SELECT dept, COUNT(*) AS n, SUM(salary) AS total FROM emp GROUP BY dept ORDER BY dept;"
QUERY_OUT = "dept\tn\ttotal\neng\t2\t220\nops\t2\t90\n"

CASES = []  # (name, body, expected status id, check(result) or None)


def case(name, lang_id, src, expect, stdin="", expected_output=None, check=None, **extra):
    body = {"language_id": lang_id, "source_code": src, "stdin": stdin, **extra}
    if expected_output is not None:
        body["expected_output"] = expected_output
    CASES.append((name, body, expect, check))


for lang, (lid, src) in SUM.items():
    case(f"{lang}/sum", lid, src, ACCEPTED, stdin="20 22\n", expected_output="42\n")

# Languages with no useful stdin: a query, a file that is its own output, and
# a program that writes with system calls.
case("sqlite/query", 82, "CREATE TABLE t (n INT);\nINSERT INTO t VALUES (20), (22);\nSELECT SUM(n) FROM t;", ACCEPTED, expected_output="42")
case("sqlite/syntax-error", 82, "SELEC 1;", NZEC)
case("text/echo", 43, "42\n", ACCEPTED, expected_output="42")
case("assembly/write", 45, 'global _start\nsection .text\n_start:\n  mov rax, 1\n  mov rdi, 1\n  mov rsi, msg\n  mov rdx, 3\n  syscall\n  mov rax, 60\n  xor rdi, rdi\n  syscall\nsection .data\nmsg: db "42", 10\n', ACCEPTED, expected_output="42")

# One compile error, one run-time error and one endless loop from each of
# the shared services.
case("kotlin/compile-error", 78, "fun main() { val x: Int = \"s\" }", COMPILE)
case("swift/compile-error", 83, 'let x: Int = "s"', COMPILE)
case("haskell/compile-error", 61, 'main :: IO ()\nmain = putStrLn (1 :: Int)', COMPILE)
case("dart/compile-error", 90, "void main() { int x = 's'; }", COMPILE)
case("ruby/exception", 72, "exit 3", NZEC, check=lambda r: r.get("exit_code") == 3 or "exit_code should be 3")
case("ruby/time-limit", 72, "loop { }", TLE)
case("kotlin/time-limit", 78, "fun main() { var x = 0L; while (true) x++ }", TLE)
case("swift/time-limit", 83, "var x = 0\nwhile true { x &+= 1 }", TLE)
case("elixir/time-limit", 57, "defmodule L do\n  def spin(n), do: spin(n + 1)\nend\nL.spin(0)", TLE)
case("fortran/time-limit", 59, "program l\n  integer(8), volatile :: x\n  x = 0\n  do\n    x = x + 1\n  end do\nend program l", TLE)

# A correct program with the wrong expected output must not pass.
case("python/wrong-answer", 71, SUM["python"][1], WRONG, stdin="1 2", expected_output="4")
case("cpp/wrong-answer", 54, SUM["cpp"][1], WRONG, stdin="1 2", expected_output="4")

# Limits, Codeforces style: 2 s of CPU, 256 MB unless raised (512 MB at most).
case("cpp/time-limit", 54, "int main(){ volatile unsigned long x = 0; for(;;) x++; }", TLE)
case("python/time-limit", 71, "while True: pass", TLE)
case("java/time-limit", 62, "public class Main { public static void main(String[] a) { long x = 0; while (true) x++; } }", TLE)
case("python/sleep-is-not-free", 71, "import time\ntime.sleep(60)", TLE)
ALLOC = '#include <bits/stdc++.h>\nint main(){ std::vector<char*> v; for(int i=0;i<%d;i++){ char* p=(char*)malloc(1<<20); memset(p,1,1<<20); v.push_back(p);} puts("ok"); }'
case("cpp/memory-limit-256", 54, ALLOC % 400, MLE)
case("cpp/200mb-fits-in-256", 54, ALLOC % 200, ACCEPTED, expected_output="ok")
case("cpp/400mb-fits-in-512", 54, ALLOC % 400, ACCEPTED, expected_output="ok", memory_limit=524288)
case("python/memory-limit", 71, "x = bytearray(400 * 1024 * 1024)\nprint(len(x))", MLE)
case("java/memory-limit", 62, "import java.util.*;\npublic class Main { public static void main(String[] a) { List<byte[]> l = new ArrayList<>(); for (int i = 0; i < 400; i++) { byte[] b = new byte[1 << 20]; Arrays.fill(b, (byte) 1); l.add(b); } System.out.println(l.size()); } }", None,
     check=lambda r: r["status"]["id"] in (MLE, NZEC) or "expected MLE or OutOfMemoryError exit")

case("cpp/compile-error", 54, "int main(){ return undefined_name; }", COMPILE,
     check=lambda r: "undefined_name" in (r.get("compile_output") or "") or "compile_output should name the error")
case("typescript/type-error", 74, 'const n: number = "nope";\nconsole.log(n);', COMPILE)
case("java/compile-error", 62, "public class Main { public static void main(String[] a) { int x = ; } }", COMPILE)
case("rust/compile-error", 73, "fn main() { let x: i32 = \"s\"; }", COMPILE)
case("cpp/segfault", 54, "int main(){ int* p = nullptr; *p = 1; }", SEGV)
case("python/exception", 71, "raise SystemExit(3)", NZEC,
     check=lambda r: r.get("exit_code") == 3 or "exit_code should be 3")
case("cpp/deep-recursion", 54, '#include <cstdio>\nlong f(long n){ volatile char pad[48]; pad[0]=1; return n ? 1 + f(n-1) + pad[0] - 1 : 0; }\nint main(){ printf("%ld\\n", f(2000000)); }', ACCEPTED, expected_output="2000000")

# Output and scratch files live in memory, so both are capped.
case("python/output-limit", 71, "while True: print('x' * 1000)", XFSZ)
case("python/disk-limit", 71, "import os\nfor i in range(600):\n    open(f'/tmp/f{i}', 'wb').write(os.urandom(1000000))\nprint('wrote 600 MB')", XFSZ,
     check=lambda r: "disk" in (r.get("message") or "") or "message should say disk limit")
case("cpp/fork-bomb", 54, "#include <unistd.h>\nint main(){ for(;;) fork(); }", None,
     check=lambda r: r["status"]["id"] in (TLE, MLE) or "a fork bomb must end as TLE or MLE")

# The sandbox has no network and none of the host's credentials.
case("python/no-network", 71,
     "import urllib.request\ntry:\n    urllib.request.urlopen('http://metadata.google.internal/computeMetadata/v1/', timeout=3)\n    print('REACHED')\nexcept Exception as e:\n    print('blocked')",
     ACCEPTED, expected_output="blocked")
case("python/no-host-env", 71, "import os\nprint(sorted(k for k in os.environ if k.startswith(('K_', 'GOOGLE', 'AUTH', 'INTERNAL'))))",
     ACCEPTED, expected_output="[]")

for name, lid in (("postgresql", 201), ("mysql", 202)):
    case(f"{name}/query", lid, QUERY, ACCEPTED, stdin=FIXTURE, expected_output=QUERY_OUT)
    case(f"{name}/syntax-error", lid, "SELEC 1;", NZEC, stdin=FIXTURE)
    case(f"{name}/missing-table", lid, "SELECT * FROM nope;", NZEC, stdin=FIXTURE)
case("postgresql/slow-query", 201, "SELECT pg_sleep(30);", TLE)
case("mysql/slow-query", 202, "SELECT SLEEP(30);", TLE)
# Each test gets its own database: a second submission must not see the first one's tables.
case("postgresql/isolated-db", 201, "SELECT count(*) AS n FROM pg_tables WHERE schemaname = 'public';", ACCEPTED, expected_output="n\n0\n")
case("mysql/isolated-db", 202, "SELECT COUNT(*) AS n FROM information_schema.tables WHERE table_schema = DATABASE();", ACCEPTED, expected_output="n\n0\n")
case("postgresql/no-superuser", 201, "COPY (SELECT 1) TO PROGRAM 'id';", NZEC)
case("mysql/no-file-access", 202, "SELECT LOAD_FILE('/etc/passwd') IS NULL AS blocked;", ACCEPTED, expected_output="blocked\n1\n")


def run_cases(filters):
    failed = 0
    print(f"{'case':<28} {'verdict':<26} {'time':>6} {'mem KB':>8} {'box ms':>7} {'api ms':>7} warm  result")
    for name, body, expect, check in CASES:
        if filters and not any(f in name for f in filters):
            continue
        code, r, t = http("POST", "/submissions?wait=true&fields=*", body)
        if code == 422 and isinstance(r, dict) and "doesn't exist" in " ".join(r.get("language_id", [])):
            # A copy of the judge may deploy only some language services.
            print(f"{name:<28} skipped: language {body['language_id']} is not deployed here")
            continue
        if code != 201 or not r:
            print(f"{name:<28} HTTP {code}: {str(r)[:90]}")
            failed += 1
            continue
        got = r["status"]["id"]
        problem = None
        if expect is not None and got != expect:
            problem = f"expected status {expect}"
        elif check:
            res = check(r)
            if res is not True:
                problem = res
        if problem:
            failed += 1
            detail = (r.get("stderr") or r.get("compile_output") or r.get("message") or r.get("stdout") or "")
            problem += " | " + detail.strip().replace("\n", " / ")[:160]
        print(f"{name:<28} {r['status']['description']:<26} {r.get('time') or '-':>6} {str(r.get('memory') or '-'):>8} "
              f"{t.get('box', '-'):>7} {t.get('total', '-'):>7} {t.get('warm', '-'):<5} {'ok' if not problem else 'FAIL: ' + problem}")
    return failed


def check_api():
    """API behaviour that is not about one language."""
    failed = 0

    def expect(label, cond, detail=""):
        nonlocal failed
        print(f"{label:<60} {'ok' if cond else 'FAIL ' + str(detail)[:200]}")
        failed += 0 if cond else 1

    code, r, _ = http("POST", "/submissions?wait=true", {"language_id": 71, "source_code": "print(1)", "cpu_time_limit": 3})
    expect("cpu_time_limit above 2 s is rejected (422)", code == 422 and "cpu_time_limit" in (r or {}), (code, r))
    code, r, _ = http("POST", "/submissions?wait=true", {"language_id": 71, "source_code": "print(1)", "memory_limit": 600000})
    expect("memory_limit above 512 MB is rejected (422)", code == 422 and "memory_limit" in (r or {}), (code, r))
    code, r, _ = http("POST", "/submissions", {"language_id": 424242, "source_code": "x"})
    expect("unknown language is rejected (422)", code == 422 and "language_id" in (r or {}), (code, r))
    code, r, _ = http("GET", "/submissions/00000000-0000-4000-8000-000000000000")
    expect("unknown token is 404", code == 404, (code, r))

    b64 = lambda s: base64.b64encode(s.encode()).decode()
    code, r, _ = http("POST", "/submissions?wait=true&base64_encoded=true",
                      {"language_id": 71, "source_code": b64("print(input()[::-1])"), "stdin": b64("héllo"), "expected_output": b64("olléh")})
    ok = code == 201 and r and r["status"]["id"] == ACCEPTED and base64.b64decode(r["stdout"]).decode() == "olléh\n"
    expect("base64 in and out, with non-ASCII text", ok, (code, r))

    # One sandbox per submission: whatever a program leaves behind must be
    # gone for the next one. Run back to back, so the same warm instance
    # serves them.
    marker = "left-" + os.urandom(6).hex()
    places = "('/tmp', '/var/tmp', '/dev/shm', os.getcwd())"
    plant = (f"import os\nwrote = 0\nfor d in {places}:\n    try:\n        open(os.path.join(d, '{marker}'), 'w').write('x'); wrote += 1\n"
             "    except OSError:\n        pass\nprint(wrote > 0)")
    look = f"import os\nprint([d for d in {places} if os.path.exists(os.path.join(d, '{marker}'))])"
    code, r, _ = http("POST", "/submissions?wait=true", {"language_id": 71, "source_code": plant})
    planted = code == 201 and (r.get("stdout") or "").strip() == "True"
    seen = []
    for _ in range(4):
        code, r, _ = http("POST", "/submissions?wait=true", {"language_id": 71, "source_code": look})
        seen.append(((r or {}).get("stdout") or "").strip())
    expect("a file left by one submission is gone for the next", planted and seen == ["[]"] * 4, (planted, seen))

    # Async single submission: token now, result by polling.
    code, r, _ = http("POST", "/submissions", {"language_id": 71, "source_code": "print(6 * 7)", "expected_output": "42"})
    token = (r or {}).get("token")
    expect("async create returns a token (201)", code == 201 and bool(token), (code, r))
    done = poll([token]) if token else {}
    expect("async submission finishes Accepted", done.get(token, {}).get("status", {}).get("id") == ACCEPTED, done)

    # A batch of one program with different inputs, plus one different program.
    subs = [{"language_id": 54, "source_code": SUM["cpp"][1], "stdin": f"{i} {i}", "expected_output": str(2 * i)} for i in range(1, 7)]
    subs.append({"language_id": 71, "source_code": "print('other')", "expected_output": "other"})
    subs.append({"language_id": 54, "source_code": SUM["cpp"][1], "stdin": "5 5", "expected_output": "11"})  # wrong on purpose
    subs.append({"language_id": 999999, "source_code": "x"})  # invalid entry
    t0 = time.time()
    code, r, _ = http("POST", "/submissions/batch", {"submissions": subs})
    tokens = [x.get("token") for x in (r or [])]
    expect("batch create: tokens for valid entries, errors for invalid", code == 201 and len(tokens) == 9 and all(tokens[:8]) and tokens[8] is None and "language_id" in r[8], (code, r))
    done = poll([t for t in tokens if t])
    ids = [done.get(t, {}).get("status", {}).get("id") for t in tokens[:8]]
    expect(f"batch results after {time.time() - t0:.1f}s: 7 Accepted, 1 Wrong Answer", ids == [ACCEPTED] * 7 + [WRONG], ids)
    outs = [(done.get(t, {}).get("stdout") or "").strip() for t in tokens[:6]]
    expect("batch outputs line up with their inputs", outs == [str(2 * i) for i in range(1, 7)], outs)

    failed += check_tokens_and_dashboard(expect)
    check_network(expect)
    return failed


def http_as(key, method, path, body=None):
    """Like http(), with a different key (or none)."""
    global KEY
    saved, KEY = KEY, key
    try:
        return http(method, path, body)
    finally:
        KEY = saved


def raw_get(path):
    p = subprocess.run(["curl", "-sS", "-o", "/dev/null", "-w", "%{http_code} %{content_type}", URL + path], capture_output=True, timeout=60)
    return p.stdout.decode()


def check_tokens_and_dashboard(expect):
    """Temporary tokens, the pages, and the analytics that outlive submissions."""
    expect("playground page is served", raw_get("/ui/").startswith("200 text/html"), raw_get("/ui/"))
    expect("admin page is served", raw_get("/ui/admin").startswith("200 text/html"), raw_get("/ui/admin"))

    code, r, _ = http("POST", "/admin/tokens", {"label": "e2e check", "ttl_hours": 1, "max_submissions": 2})
    temp = (r or {}).get("token", "")
    expect("admin can create a temporary token (201)", code == 201 and temp.startswith("jt_"), (code, r))
    if not temp:
        return 1
    run = {"language_id": 71, "source_code": "print(6 * 7)", "expected_output": "42"}
    code, r, _ = http_as(temp, "POST", "/submissions?wait=true", run)
    expect("temporary token can run code", code == 201 and r["status"]["id"] == ACCEPTED, (code, r))
    code, r, _ = http_as(temp, "GET", "/whoami")
    expect("temporary token sees its own allowance", code == 200 and r.get("role") == "temporary" and r.get("max_submissions") == 2, (code, r))
    code, _, _ = http_as(temp, "GET", "/admin/stats")
    expect("temporary token cannot read analytics (403)", code == 403, code)
    code, _, _ = http_as(temp, "POST", "/admin/tokens", {"label": "escalation"})
    expect("temporary token cannot create tokens (403)", code == 403, code)
    http_as(temp, "POST", "/submissions?wait=true", run)
    # The cap is exact on one API instance and can lag by a few on several,
    # so allow a couple of attempts before insisting.
    codes = [http_as(temp, "POST", "/submissions?wait=true", run)[0] for _ in range(4)]
    expect("temporary token stops at its submission cap (429)", 429 in codes, codes)
    code, r, _ = http_as("jt_0000000000000000000000000000000000000000", "GET", "/languages")
    expect("unknown temporary token is refused (401)", code == 401, code)
    code, r, _ = http("POST", "/admin/tokens", {"label": "too long", "ttl_hours": 200})
    expect("a token cannot outlive 7 days (422)", code == 422, (code, r))

    code, rows, _ = http("GET", "/admin/tokens")
    mine = [t for t in (rows or []) if t.get("label") == "e2e check"]
    expect("token list shows label and usage, never the token", code == 200 and mine and "token" not in mine[0], (code, mine[:1]))
    for t in mine:
        code, _, _ = http("DELETE", "/admin/tokens/" + t["id"])
    expect("admin can revoke a token (204)", code == 204, code)

    # Counters are written every 10 s per instance; give them a moment.
    total = 0
    for _ in range(8):
        code, r, _ = http("GET", "/admin/stats?range=24h")
        if code == 200:
            total = sum(c.get("n", 0) for b in r["buckets"] for c in b["l"].values())
            if total > 0:
                break
        time.sleep(4)
    ok = code == 200 and len(r["buckets"]) == 24 and total > 0 and "latency_bounds_ms" in r
    expect(f"analytics: 24 hourly buckets holding {total} submissions", ok, (code, total))
    code, r, _ = http("GET", "/admin/stats?range=90d")
    expect("analytics: 90 daily buckets", code == 200 and len(r["buckets"]) == 90, code)
    return 0


# A file on a host that is unlikely to go away, 69 MB.
NET_FILE = "https://dl.google.com/go/go1.22.0.linux-amd64.tar.gz"
NET_FETCH = """
import sys, time, urllib.request
times, total = int(sys.argv[1]), 0
for _ in range(times):
    r = urllib.request.urlopen(%r, timeout=50)
    while True:
        chunk = r.read(1 << 16)
        if not chunk: break
        total += len(chunk)
print(total // 1000000, "MB")
""" % NET_FILE
NET_PING = "import urllib.request\nprint(urllib.request.urlopen(%r, timeout=10).status)" % (NET_FILE + ".sha256")
NET_FIELDS = "/submissions?wait=true&fields=status,stdout,stderr,message,time,wall_time,enable_network,network_time_limit"


def check_network(expect):
    """enable_network: off unless the deployment allows it, and limited when it does."""
    code, cfg, _ = http("GET", "/config_info")
    allowed = bool((cfg or {}).get("allow_enable_network"))
    ask = {"language_id": 71, "source_code": NET_PING, "enable_network": True}

    if not allowed:
        code, r, _ = http("POST", NET_FIELDS, ask)
        ok = code == 201 and r["status"]["id"] == NZEC and r["enable_network"] is False
        expect("network is off here: enable_network is ignored, the run has none", ok, (code, r))
        code, r, _ = http("POST", "/admin/tokens", {"label": "e2e net", "ttl_hours": 1, "network": True})
        expect("network is off here: no token can be given it (422)", code == 422, (code, r))
        return

    code, r, _ = http("POST", NET_FIELDS, {**ask, "expected_output": "200"})
    ok = code == 201 and r["status"]["id"] == ACCEPTED and r["enable_network"] is True and r["network_time_limit"] is not None
    expect("network: a run that asks for it can fetch a URL", ok, (code, r))
    code, r, _ = http("POST", NET_FIELDS, {"language_id": 71, "source_code": NET_PING})
    expect("network: a run that does not ask has none", code == 201 and r["status"]["id"] == NZEC and r["enable_network"] is False, (code, r))

    limit_mb = (cfg.get("max_download_size") or 0) // 1024
    fits = max(1, min(limit_mb // 69, 1))
    over = limit_mb // 69 + 2
    if limit_mb >= 69:
        code, r, _ = http("POST", NET_FIELDS, {**ask, "source_code": NET_FETCH, "command_line_arguments": str(fits), "expected_output": "68 MB"})
        ok = code == 201 and r["status"]["id"] == ACCEPTED and float(r["time"]) < 0.5
        expect(f"network: a 69 MB download passes and is not charged as CPU time ({r.get('time')} s)", ok, (code, r))
    code, r, _ = http("POST", NET_FIELDS, {**ask, "source_code": NET_FETCH, "command_line_arguments": str(over)})
    ok = code == 201 and r["status"]["id"] == XFSZ and "download limit" in (r.get("message") or "")
    expect(f"network: downloading {over * 69} MB is stopped at the {limit_mb} MB limit", ok, (code, r))

    code, r, _ = http("POST", NET_FIELDS, {**ask, "source_code": "import time\ntime.sleep(8)\nprint('late')", "network_time_limit": 5, "expected_output": "late"})
    expect("network: waiting is allowed for wall time plus network time (8 s of 10)", code == 201 and r["status"]["id"] == ACCEPTED, (code, r))
    code, r, _ = http("POST", NET_FIELDS, {**ask, "source_code": "import time\ntime.sleep(30)", "network_time_limit": 3})
    ok = code == 201 and r["status"]["id"] == TLE and 7.5 < float(r["wall_time"]) < 9.5
    expect(f"network: and no longer (stopped after {r.get('wall_time')} s of 8)", ok, (code, r))
    code, r, _ = http("POST", NET_FIELDS, {**ask, "source_code": "while True: pass"})
    expect("network: the CPU limit still applies", code == 201 and r["status"]["id"] == TLE and float(r["time"]) < 2.5, (code, r))
    code, r, _ = http("POST", NET_FIELDS, {**ask, "network_time_limit": (cfg.get("max_network_time_limit") or 60) + 1})
    expect("network: network_time_limit above the maximum is rejected (422)", code == 422 and "network_time_limit" in (r or {}), (code, r))

    code, r, _ = http("POST", "/admin/tokens", {"label": "e2e net plain", "ttl_hours": 1, "max_submissions": 3})
    plain = (r or {}).get("token", "")
    code, r, _ = http("POST", "/admin/tokens", {"label": "e2e net granted", "ttl_hours": 1, "max_submissions": 3, "network": True})
    granted = (r or {}).get("token", "")
    code, r, _ = http_as(plain, "POST", NET_FIELDS, ask)
    expect("network: a temporary token without the right is refused (422)", code == 422 and "enable_network" in (r or {}), (code, r))
    code, r, _ = http_as(granted, "POST", NET_FIELDS, ask)
    expect("network: a temporary token created with the right may use it", code == 201 and r["status"]["id"] == ACCEPTED, (code, r))
    code, rows, _ = http("GET", "/admin/tokens")
    for t in rows or []:
        if t.get("label", "").startswith("e2e net"):
            http("DELETE", "/admin/tokens/" + t["id"])


def poll(tokens, timeout=120):
    deadline = time.time() + timeout
    out = {}
    while time.time() < deadline:
        code, r, _ = http("GET", "/submissions/batch?tokens=" + ",".join(tokens))
        subs = (r or {}).get("submissions") or []
        out = {s["token"]: s for s in subs if s}
        if len(out) == len(tokens) and all(s["status"]["id"] > 2 for s in out.values()):
            return out
        time.sleep(1)
    return out


if __name__ == "__main__":
    filters = [a for a in sys.argv[1:] if a != "api"]
    only_api = sys.argv[1:] == ["api"]
    failed = 0
    if not only_api:
        failed += run_cases(filters)
    if only_api or not filters:
        print()
        failed += check_api()
    print(f"\n{'ALL PASSED' if not failed else str(failed) + ' FAILED'}")
    sys.exit(1 if failed else 0)
