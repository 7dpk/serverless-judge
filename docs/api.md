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
`compiler_options`, `command_line_arguments` and
`redirect_stderr_to_stdout`.

A `wait=true` response carries a `Server-Timing` header that says where the
time went.

## Language ids

| Language | ids | | Language | ids |
|---|---|---|---|---|
| C (GCC 14) | 50, 103 | | Go 1.26 | 60, 107 |
| C++ (GCC 14, C++20) | 54, 105 | | PHP 8.4 | 68, 98 |
| Python 3.13 | 71, 100 | | Rust (stable) | 73, 108 |
| JavaScript (Node 24) | 63, 102 | | C# (.NET 10) | 51 |
| TypeScript 7 | 74, 101 | | PostgreSQL 18 | 201 |
| Java 25 | 62, 91 | | MySQL 8.4 | 202 |

Ids are set in each `languages/<family>/langs/<name>.json`. If your client
already uses some other id for a language, add it to the `ids` list and
redeploy that family.

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
- **Java** source must declare `class Main`.
- **`GET /languages`** adds a `key` to each entry (`python`, `cpp`, ...) so
  a client can tell which ids are the same language.

## Temporary tokens

For letting someone else try the judge without handing over the admin key.
A temporary token can submit and read submissions and nothing else. Only
its hash is stored, so it is shown once.

```bash
curl -s -X POST "$JUDGE_URL/admin/tokens" -H "X-Auth-Token: $JUDGE_KEY" -H 'Content-Type: application/json' \
  -d '{"label": "QA team", "ttl_hours": 24, "max_submissions": 500}'
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
