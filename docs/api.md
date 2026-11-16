# API

The API follows [Judge0 CE](https://ce.judge0.com): same paths, fields,
status ids and `X-Auth-Token` header. If you have a Judge0 client, point it
at the new URL and read [the differences](#differences-from-judge0) once.

## Setup

```bash
# deployed with Terraform
export JUDGE_URL=$(terraform -chdir=terraform output -raw url)
export JUDGE_KEY=$($(terraform -chdir=terraform output -raw api_key_command))

# deployed with the script
export JUDGE_URL=$(./deploy.sh status | awk '/judge-api/ {print $2}')
export JUDGE_KEY=$(./deploy.sh key)
```

## Submissions

```bash
# Run and wait for the result
curl -s "$JUDGE_URL/submissions?wait=true" -H "X-Auth-Token: $JUDGE_KEY" -H 'Content-Type: application/json' \
  -d '{"language_id": 71, "source_code": "print(int(input()) * 2)", "stdin": "21", "expected_output": "42"}'

# Queue it, then poll
curl -s "$JUDGE_URL/submissions" -H "X-Auth-Token: $JUDGE_KEY" -H 'Content-Type: application/json' \
  -d '{"language_id": 54, "source_code": "..."}'            # -> {"token": "..."}
curl -s "$JUDGE_URL/submissions/<token>" -H "X-Auth-Token: $JUDGE_KEY"

# Batch (up to 100)
#   POST /submissions/batch
#   GET  /submissions/batch?tokens=a,b,c
```

Also supported: `GET /languages`, `/statuses`, `/config_info`, `/whoami`,
`?base64_encoded=true`, `?fields=...`, `callback_url`, `additional_files`,
`compiler_options`, `command_line_arguments`,
`redirect_stderr_to_stdout` and, where the deployment allows it,
[`enable_network`](#network-access).

A `wait=true` response carries a `Server-Timing` header that says where the
time went.

## Language ids

| Language | ids | Service |
|---|---|---|
| C (GCC 14, C17) | 50, 103 | `gcc` |
| C++ (GCC 14, C++20) | 54, 105 | `gcc` |
| Python (3.13) | 71, 100 | `python` |
| JavaScript (Node.js 24) | 63, 102 | `node` |
| TypeScript (7.0, Node.js 24) | 74, 101 | `node` |
| Java (OpenJDK 25) | 62, 91 | `java` |
| C# (.NET 10) | 51 | `dotnet` |
| Go (1.26) | 60, 107 | `go` |
| PHP (8.4) | 68, 98 | `php` |
| Rust (stable, edition 2024) | 73, 108 | `rust` |
| PostgreSQL (18) | 201 | `postgres` |
| MySQL (8.4) | 202 | `mysql` |
| Plain Text | 43 | `script` |
| Bash (5.2) | 46 | `script` |
| Lua (5.4) | 64 | `script` |
| Ruby (3.3) | 72 | `script` |
| R (4.5) | 80, 99 | `script` |
| SQL (SQLite 3.46) | 82 | `script` |
| Perl (5.40) | 85 | `script` |
| Kotlin (2.4) | 78, 111 | `jvm` |
| Scala (3.3) | 81 | `jvm` |
| Clojure (1.12) | 86 | `jvm` |
| Groovy (6.0) | 88 | `jvm` |
| Swift (6.4) | 83 | `swift` |
| Elixir (1.18) | 57 | `functional` |
| Erlang (OTP 27) | 58 | `functional` |
| Haskell (GHC 9.6) | 61 | `functional` |
| OCaml (5.3) | 65 | `functional` |
| Assembly (NASM 2.16, x86-64) | 45 | `native` |
| D (GDC 14) | 56 | `native` |
| Fortran (GFortran 14) | 59 | `native` |
| Pascal (FPC 3.2) | 67 | `native` |
| C (Clang 19, C17) | 75, 104, 110 | `native` |
| C++ (Clang 19, C++20) | 76 | `native` |
| COBOL (GnuCOBOL 3) | 77 | `native` |
| Objective-C (Clang 19, GNUstep) | 79 | `native` |
| Dart (3.14) | 90 | `native` |

The service is the folder under `languages/` whose image has the language.
Languages in the same service share its instances.

Ids are set in each `languages/<family>/langs/<name>.json`. If your client
already uses some other id for a language, add it to the `ids` list and
redeploy that family.

## Network access

A sandbox has no network. A submission can ask for one with Judge0's own
field:

```bash
curl -s "$JUDGE_URL/submissions?wait=true" -H "X-Auth-Token: $JUDGE_KEY" -H 'Content-Type: application/json' \
  -d '{"language_id": 71, "enable_network": true, "network_time_limit": 45,
       "source_code": "import urllib.request\nprint(len(urllib.request.urlopen(\"https://example.com\").read()))"}'
```

It is off unless the deployment turns it on (`allow_network = true` in
Terraform, `ALLOW_NETWORK=1` with `deploy.sh`). Where it is off, the field
is ignored as it always was: the program runs without a network and the
submission reads `enable_network: false`. `GET /config_info` says which
(`allow_enable_network`) and lists the limits.

Where it is on:

| | Default | |
|---|---|---|
| Who may ask | admin keys | A temporary token only if it was created with `"network": true`; otherwise 422 |
| Download size | 125 MB | What a run may receive in total, and the largest file it may write. Past it the run ends as Runtime Error (SIGXFSZ) with the message "download limit exceeded", or with the program's own "File too large" error |
| Network time | 30 s, at most 60 | Added to `wall_time_limit`, for time spent waiting on the network. Set per submission with `network_time_limit` |
| CPU time | unchanged | Receiving data costs CPU that isn't the program's work: 6 to 14 ms per MB when measured. 15 ms per MB received is added to the limit and taken off `time`, so a run that only downloads reads close to 0 |

The deployment's side of these is `network_time_limit`,
`max_network_time_limit` and `max_download_mb` (see
[Deploying](deploying.md#with-terraform)).

What such a run can and can't reach, as measured: any public host on any
TCP port except 25, over IPv4. Not the metadata server, not the runner that
hosts the sandbox, and nothing in a VPC, because the runner services aren't
attached to one. Cloud Run has no filter to narrow that, so treat the right
to ask for network access as the right to make requests to the internet
from your project, and give it to callers you trust.
[Architecture](architecture.md#safety) has more on how the limits are
enforced.

A run with network access gets a sandbox of its own, created for it, so it
starts about 0.2 s later than one that takes a waiting sandbox, and
submissions with it are never grouped.

## Differences from Judge0

- **Status 15, Memory Limit Exceeded.** Judge0 has no such status.
- **Limits are capped** at 2 s CPU and 512 MB (`MAX_CPU_TIME`,
  `MAX_MEMORY_KB` on judge-api). A request above the cap is rejected with
  422.
- **Memory** is the program's anonymous memory (heap and stacks). Runtime
  code mapped from disk is not counted, so an idle JVM reads about 48 MB.
  The sandbox accounts memory in 2 MB steps.
- **Time** has 10 ms resolution.
- **SQL.** `source_code` is the script to judge. `stdin` is the fixture
  (schema and seed data), run first in a fresh database. Output is
  tab-separated with a header row, `NULL` spelled out. The time limit is
  wall-clock.
- **TypeScript** is type-checked; a type error is a Compilation Error.
- **Java** source must declare `class Main`. Kotlin is compiled as
  `Main.kt` (so `fun main` becomes `MainKt`), and Scala must define
  `object Main`.
- **Slow starters.** For Elixir, Erlang, Clojure and Groovy, an allowance
  for the CPU their runtime needs just to start is added to the limit and
  taken off `time`, so the 2 s limit applies to the program: 0.6 s for
  Erlang, 1.0 s for Elixir and Clojure, 1.5 s for Groovy. An empty program
  costs about that much on an instance that has just started and less on a
  warm one, so a small program often reads 0.
- **SQL (SQLite)** runs `source_code` against an empty `db.sqlite`, or
  against the one in `additional_files`, as Judge0 does. `stdin` is not
  read. The two database servers work differently: see the SQL entry above.
- **Objective-C** is Clang with GNUstep's Foundation on the GNU runtime, as
  in Judge0: literals and `@autoreleasepool` work, ARC and object
  subscripting don't.
- **COBOL** programs print one line about libxml versions on `stderr`. It
  comes from Debian's GnuCOBOL package, not from the program.
- **`enable_network`** is ignored unless the deployment allows it, and has
  limits when it does: see [Network access](#network-access).
  `network_time_limit` is an extra field.
- **`GET /languages`** adds a `key` to each entry (`python`, `cpp`, ...) so
  a client can tell which ids are the same language.

## Temporary tokens

For letting someone else try the judge without handing over the admin key.
A temporary token can submit and read submissions and nothing else. Only
its hash is stored, so it is shown once.

```bash
curl -s -X POST "$JUDGE_URL/admin/tokens" -H "X-Auth-Token: $JUDGE_KEY" -H 'Content-Type: application/json' \
  -d '{"label": "QA team", "ttl_hours": 24, "max_submissions": 500}'    # add "network": true to allow enable_network
curl -s "$JUDGE_URL/admin/tokens" -H "X-Auth-Token: $JUDGE_KEY"          # label, expiry, usage
curl -s -X DELETE "$JUDGE_URL/admin/tokens/<id>" -H "X-Auth-Token: $JUDGE_KEY"
```

A token lives for 7 days at most. The submission cap is exact on one API
instance and can be overshot by a few submissions when several instances
serve the same token at once. A revoked token can keep working for up to 30
seconds.

The admin page at `/ui/admin` does the same with buttons, and gives you a
link that opens the playground already signed in.

## Analytics

`GET /admin/stats?range=24h|7d|30d|90d` returns what the dashboard at
`/ui/admin` draws: submissions over time, time from submit to result
(median and 90th percentile), verdicts, and a per-language table.
