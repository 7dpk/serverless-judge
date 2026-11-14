# serverless-judge

A Judge0-compatible code execution service on Google Cloud Run. Every
submission runs in a throwaway [Cloud Run sandbox](https://docs.cloud.google.com/run/docs/code-execution)
(gVisor, no network, no credentials). With no traffic there are no
instances: compute is billed per request, and the only standing cost is
image storage ([under 20 cents a month](#cost)).

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/how-it-works-dark.gif">
    <img src="docs/how-it-works.gif" width="880" alt="Animation. With no traffic every service is at zero instances and compute billing is off. A submission starts the API and a runner; it runs in its own sandbox, which is destroyed afterwards while the next one is already waiting. A burst is queued through Pub/Sub and the runners scale out. When traffic stops, billing stops and everything returns to zero.">
  </picture>
</p>

Ten languages plus two SQL engines: C, C++, Python, JavaScript, TypeScript,
Java, C#, Go, PHP, Rust, PostgreSQL, MySQL.

Limits are Codeforces style: 2 s of CPU and 256 MB by default, never more
than 2 s and 512 MB.

## Quick start

You need a Google Cloud project with billing enabled, the
[gcloud CLI](https://cloud.google.com/sdk/docs/install) and
[Terraform](https://developer.hashicorp.com/terraform/install) 1.5 or newer.

```bash
gcloud auth login                         # used for the image builds
gcloud auth application-default login     # used by Terraform

cd terraform
terraform init
terraform apply -var project=<your-project-id>
```

A first apply takes about 11 minutes, most of it image builds (10 min 57 s
measured in `us-east1`). When it finishes:

```bash
terraform output playground               # open this in a browser
$(terraform output -raw api_key_command)  # the key to paste into it
```

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/playground-dark.gif">
    <img src="docs/playground.gif" width="880" alt="Recording of the playground: a Python program, a C++ program and a PostgreSQL query each run and come back Accepted with run time and memory, then Run all checks all twelve languages.">
  </picture>
</p>

The recording is a real session against a deployed judge. "Round trip"
includes the network between the browser and the region.

More on Terraform, and a script that does the same without it, under
[Deploying](#deploying).

## How it works

The animation above, as text:

```
                      wait=true
client ─X-Auth-Token─> judge-api ───────────────────────────┐
                         │ wait=false                       ▼
                         └─> Pub/Sub ─push─> judge-worker ─> runner-<family> ─ sandbox ─ judge box ─ jexec ─ program
                Firestore: submissions, language registry
```

- **judge-api** (`api/`, `ROLE=api`) is public. It validates submissions,
  stores them in Firestore, and decides the verdict by comparing output on
  its side. Expected output never enters a sandbox. It runs `wait=true`
  submissions itself and publishes the rest to Pub/Sub.
- **judge-worker** (same image, `ROLE=worker`) is private. Pub/Sub pushes
  each queued group to it and it runs them. It is a separate service so a
  backlog of queued work cannot use up the capacity that answers API callers.
- **runner-\<family\>** (`languages/<family>/`) is one Cloud Run service per
  toolchain image, concurrency 1, with a service account that has no roles.
  Only the API's identity may invoke it.
- **judge** (`runner/`) is one static binary in every image. `judge serve`
  is the HTTP server. For each job it starts `sandbox do -- judge box`, the
  supervisor that compiles, runs each test under limits and reports back.
  `jexec` is a tiny C launcher that applies rlimits and measures the program.
- One sandbox serves exactly one job and is then destroyed. The runner
  creates the next sandbox while the current job runs, so a job normally
  waits 0 ms for one.
- A batch of the same program with different inputs is compiled once and run
  as several tests in a single sandbox.
- When there is more work than runners, it waits in order at a per-language
  gate inside the API or worker, as wide as that language's instance cap.
  One busy language does not slow the others. `api/dispatch.go` explains the
  design and the two that were tried and measured before it.

## Playground and admin page

Both are served by judge-api itself; nothing is loaded from another site.

- **`/ui/`** is the playground: pick a language, edit the sample, run it, and
  see the verdict, output, run time and memory. "Check every language" runs
  all twelve samples and compares their output.
- **`/ui/admin`** needs an admin key (the `api_key_command` Terraform
  output, or `./deploy.sh key`). It has the analytics dashboard and
  temporary tokens.

**Temporary tokens** are for letting someone else try the judge. Create one
on the admin page with a label, a lifetime (at most 7 days) and a cap on
submissions, then send the link it gives you: it opens the playground
already signed in. A temporary token can submit and read submissions and
nothing else. Only its hash is stored, so it is shown once.

```bash
curl -s -X POST "$JUDGE_URL/admin/tokens" -H "X-Auth-Token: $JUDGE_KEY" -H 'Content-Type: application/json' \
  -d '{"label": "QA team", "ttl_hours": 24, "max_submissions": 500}'
curl -s "$JUDGE_URL/admin/tokens" -H "X-Auth-Token: $JUDGE_KEY"          # label, expiry, usage
curl -s -X DELETE "$JUDGE_URL/admin/tokens/<id>" -H "X-Auth-Token: $JUDGE_KEY"
```

The cap is exact on one API instance and can be overshot by a few
submissions when several instances serve the same token at once. A revoked
token can keep working for up to 30 seconds.

**The dashboard** shows submissions over time, time from submit to result
(median and 90th percentile), verdicts, and a per-language table, for the
last 24 hours to 90 days. It reads `GET /admin/stats?range=24h|7d|30d|90d`.

## Data retention

- **Submissions** (code, input, output, verdict) expire 6 days after they
  are created and are deleted by Firestore within about a day of that, so
  nothing is stored beyond 7 days. An expired submission answers 404
  straight away. Set `SUBMISSION_TTL_HOURS` on judge-api to keep less.
- **Analytics** are kept indefinitely. They are counters per hour and per
  day and per language: how many finished, with which verdicts, a latency
  histogram, and totals for run time and memory. Nothing in them identifies
  a submission, which is why the dashboard still works after the
  submissions are gone.
- **Temporary tokens** are deleted when they expire.
- **Logs** of the judge's services go to their own Cloud Logging bucket,
  `judge-logs`, kept for 7 days, and are excluded from the project's default
  30-day bucket. They hold request lines and timings, never code or output.
  A new routing rule took up to 4 minutes to be applied when measured, so a
  first deploy creates the services 5 minutes after the rule.
  To read them: `gcloud logging read '<filter>' --bucket judge-logs --location global --view _AllLogs`.
- **Queued messages** in Pub/Sub are dropped after 30 minutes.

## Using the API

The API follows [Judge0 CE](https://ce.judge0.com): same paths, fields,
status ids and `X-Auth-Token` header.

```bash
export JUDGE_URL=$(terraform -chdir=terraform output -raw url)
export JUDGE_KEY=$($(terraform -chdir=terraform output -raw api_key_command))
# or, if you deployed with the script:
#   JUDGE_URL=$(./deploy.sh status | awk '/judge-api/ {print $2}')  JUDGE_KEY=$(./deploy.sh key)

# Run and wait for the result
curl -s "$JUDGE_URL/submissions?wait=true" -H "X-Auth-Token: $JUDGE_KEY" -H 'Content-Type: application/json' \
  -d '{"language_id": 71, "source_code": "print(int(input()) * 2)", "stdin": "21", "expected_output": "42"}'

# Queue it, then poll
curl -s "$JUDGE_URL/submissions" -H "X-Auth-Token: $JUDGE_KEY" -H 'Content-Type: application/json' \
  -d '{"language_id": 54, "source_code": "..."}'            # -> {"token": "..."}
curl -s "$JUDGE_URL/submissions/<token>" -H "X-Auth-Token: $JUDGE_KEY"

# Batch (up to 100): POST /submissions/batch, GET /submissions/batch?tokens=a,b,c
```

Also: `GET /languages`, `/statuses`, `/config_info`, `/whoami`, `?base64_encoded=true`,
`?fields=...`, `callback_url`, `additional_files`, `compiler_options`,
`command_line_arguments`, `redirect_stderr_to_stdout`. A `wait=true` response
carries a `Server-Timing` header with where the time went.

| Language | ids | | Language | ids |
|---|---|---|---|---|
| C (GCC 14) | 50, 103 | | Go 1.26 | 60, 107 |
| C++ (GCC 14, C++20) | 54, 105 | | PHP 8.4 | 68, 98 |
| Python 3.13 | 71, 100 | | Rust (stable) | 73, 108 |
| JavaScript (Node 24) | 63, 102 | | C# (.NET 10) | 51 |
| TypeScript 7 | 74, 101 | | PostgreSQL 18 | 201 |
| Java 25 | 62, 91 | | MySQL 8.4 | 202 |

Ids are set in each `languages/<family>/langs/<name>.json`. Add any id your
caller already uses to the `ids` list and redeploy that family.

### Differences from Judge0

- **Status 15, Memory Limit Exceeded.** Judge0 has no such status.
- **Limits are capped** at 2 s CPU and 512 MB (`MAX_CPU_TIME`, `MAX_MEMORY_KB`
  on judge-api). A request above the cap is rejected with 422.
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
- **`GET /languages`** adds a `key` to each entry (`python`, `cpp`, ...) so a
  client can tell which ids are the same language.

## Deploying

There are two ways, and they read the same files: a folder under
`languages/` is a runner service, its `service.json` sizes it, and each
`langs/*.json` is a language. Use one of them for a given copy of the judge,
not both.

### With Terraform

`terraform/` creates everything from an empty project: the APIs, the image
registry, the images (built by Cloud Build, so only gcloud is needed
locally), three service accounts, the Firestore database with its expiry
rules, the Pub/Sub queue, the 7-day log bucket, the twelve Cloud Run
services and the language registry.

```bash
cd terraform
terraform init
terraform apply -var project=<your-project-id>
```

Put settings in `terraform.tfvars` (see `terraform.tfvars.example`):

| Variable | Default | |
|---|---|---|
| `project` | required | Project to deploy into |
| `region` | `us-central1` | Region for the services, the registry and the database |
| `families` | all | Deploy only some folders of `languages/`, for example `["python", "gcc"]` |
| `max_instances` | from `service.json` | Per-family instance cap, for example `{ gcc = 40 }` |
| `cpu_quota` | `200` | The regional CPU quota the caps must fit in (see [Capacity](#capacity-and-the-cpu-quota)) |
| `api_min_instances` | `0` | Keep API instances warm: no cold start, but an idle cost |
| `submission_ttl_hours` | `144` | How long submissions are kept |
| `name_prefix` | none | Run a second copy in the same project, for example `"staging-"` |
| `firestore_location` | the region | For a multi-region database such as `nam5` |
| `delete_data_on_destroy` | `false` | Let `terraform destroy` delete the database too |

- **Changing things.** Edit a package list, a `service.json` or add a
  language folder, then `terraform apply` again. Image tags are hashes of
  the files that go into them, so only what changed is rebuilt and only the
  services that use it are rolled.
- **The API key** is generated by a local step and stored in Secret
  Manager. It is never written to the Terraform state.
- **Removing it.** `terraform destroy` deletes what it created. The
  database is left in place unless `delete_data_on_destroy = true`, and the
  APIs stay enabled. Two things linger after a destroy: Firestore reserves
  a deleted database's name for about 5 minutes, so wait that long before
  applying again under the same name; and Cloud Logging keeps a deleted log
  bucket for 7 days, which the next apply restores and reuses.
- **If apply stops part way.** Run it again; it continues where it
  stopped. "Permission denied on secret" or an HTTP 403 right after a
  deploy means an access grant has not taken effect yet, which can take a
  few minutes.
- **Where it runs.** Any region with second-generation Cloud Run should
  work: the sandbox documentation listed no region limits when read on
  14 November 2026. It has been deployed and tested in `us-central1`,
  `us-east1`, `europe-west1` and `asia-south1`. Sandbox support was
  verified with version 8.11.0 of the `google-beta` provider.
- **Scale to zero is set explicitly.** Every service has
  `min_instance_count = 0` and `cpu_idle = true`. The second one matters:
  without it Terraform deploys "CPU always allocated", which bills for the
  whole life of every instance.

It runs local steps (the image builds and the key), so the machine running
`terraform apply` needs gcloud, bash and openssl.

Tested on 14 November 2026 with Terraform 1.5.7 and version 8.11.0 of the
providers, by deploying separate copies from nothing beside the
`us-central1` one. The last of them, in `us-east1` on the configuration in
this repository:

- one `terraform apply`, 103 resources, 10 min 57 s;
- the live suite (`tests/e2e.py`, 70 checks) started the moment apply
  returned and passed;
- a second `terraform plan` showed no changes;
- none of its log lines reached the default 30-day bucket;
- `terraform destroy` removed all 103 resources in 74 s.

Earlier copies in `asia-south1` and `europe-west1` covered the rest: a
burst of 300 submissions all Accepted in 13.8 s, an apply interrupted by a
dropped connection and continued by running it again, and a destroy
followed by an apply under the same names (the log bucket was restored
automatically; the database name was free after 5 minutes).

Not tested: a brand-new project (the APIs were already enabled here), and
a remote state backend.

### With deploy.sh

The same result with gcloud alone and no state file.

```bash
gcloud config set project <project>
./deploy.sh all            # infra, every language, the API
./deploy.sh lang python    # rebuild and redeploy one family
./deploy.sh api            # the API and the worker (one image)
./deploy.sh status
./deploy.sh key
```

### Adding packages

Edit the family's package list, then `terraform apply` or `./deploy.sh lang <family>`.

| Family | File |
|---|---|
| python | `requirements.txt` |
| node | `package.json` |
| java | `packages.txt` (Maven `group:artifact:version`) |
| go | `packages.txt` (`module@version`) |
| rust | `Cargo.toml` |
| php | `composer.json` |
| gcc | `apt-packages.txt` |

C# has no package list yet.

### Adding a language

Create `languages/<family>/` with:

- `Dockerfile`: the toolchain, ending with the three lines every family
  shares (copy `/judge` and `/jexec`, copy `langs/`, set the entrypoint).
- `service.json`: CPU, memory and instance limits for the Cloud Run service.
- `langs/<name>.json`: how to compile and run. Fields are documented on
  `Manifest` in `runner/proto/proto.go`. `languages/php` is the smallest
  example; `languages/postgres` shows a language that needs a daemon.

Then `terraform apply` or `./deploy.sh lang <family>`. The API needs no
redeploy: it re-reads the registry when it meets an id it does not know.

## Safety

- The program runs in a gVisor sandbox inside a second-generation Cloud Run
  instance. By default the sandbox has no network, no metadata server, an
  empty environment and a read-only filesystem apart from in-memory `/tmp`.
- One job per sandbox; one request per instance at a time.
- The runner's service account has no roles, and runners accept calls only
  from judge-api.
- The supervisor shares the sandbox with the program, so it protects its
  report rather than relying on the program behaving: it is non-dumpable, the
  report is tagged with a nonce the program never sees, and a missing or
  untagged report is an Internal Error, never a pass.
- CPU, wall time, memory, output size, file size, process count and total
  filesystem growth are all capped.

Cloud Run sandboxes are in **public preview**: no SLA, and behaviour may
change. If that becomes a problem the fallback is the first-generation
environment with the supervisor run directly; `runner/internal/serve/pool.go`
is the only place that knows about the sandbox CLI.

## Measured performance

us-central1, 2 vCPU runners, 14 November 2026. Time inside the runner for a
small program, compile included, on a warm instance.

| | Warm | Cold start |
|---|---|---|
| Python | 30-50 ms | 0.8 s |
| JavaScript | 65 ms | 2.0 s |
| PHP | 80-130 ms | 0.7-2.1 s |
| C | 100-210 ms | |
| PostgreSQL, MySQL | 300 ms | 3.5 s |
| TypeScript | 400 ms | |
| Rust | 400-750 ms | 2.4 s |
| Go | 450-750 ms | 2.5 s |
| Java | 480-600 ms | 2.1 s |
| C++ with `<bits/stdc++.h>` | 0.8-1.2 s | 3.6 s |
| C# | 1.1-1.3 s | 4.0 s |

Warm is time inside the runner; add 50 to 130 ms for the API hop and the
Firestore write. Cold start is a new instance (scaling from zero, or scaling
out), measured as time to first byte from a client outside the region, so it
includes about 0.35 s of network. Blank cells were not measured.

Bursts of different programs, starting cold, with the instance caps below:
300 Python in 10.7 s, 1,000 Python in 29 s, 200 C++ in 22 s. All verdicts
correct.

To trade idle cost for no cold starts, set `min_instances` in a family's
`service.json`.

## Capacity and the CPU quota

Cloud Run gives a project a fixed amount of CPU per region: **200 vCPU** by
default ("Total CPU allocation, per project per region" under IAM & Admin >
Quotas). Every service draws on it, and idle instances keep counting for up
to 15 minutes after their last request.

So the instance caps must fit inside the quota. Both deploy paths check
this and refuse caps that do not fit:

    runners (sum of max_instances x cpu) + API + worker + 6 for rollouts  <=  quota

The defaults use all 200: 90 runner instances at 2 vCPU (gcc 20, python 12,
java 12, node 10, dotnet 8, mysql 8, go 6, postgres 6, php 4, rust 4), 6 API
instances and 8 worker instances.

With those caps the measured ceiling is about **135 submissions per second**
for a mix of all 12 languages. For more, request a higher quota from Google,
raise `max_instances` in `languages/*/service.json`, and deploy with
`cpu_quota = <new quota>` (Terraform) or `QUOTA_VCPU=<new quota>`
(`deploy.sh`).

### Runner size: 2 vCPU or 1

Less than 1 vCPU is not possible: Cloud Run allows it only in the
first-generation environment, and sandboxes need the second. Above 1 it must
be a whole number. So the choice is 1 or 2, set per family as `cpu` in
`service.json`. Measured on 14 November 2026 at the same total vCPU, so
twice as many instances at 1 vCPU:

| | 2 vCPU, sustained | 1 vCPU, sustained | One run at 2 vCPU | One run at 1 vCPU |
|---|---|---|---|---|
| Python | 50.1 /s | 33.1 /s | about 31 ms | 34-186 ms |
| C++ | 10.9 /s | 19.2 /s | 1.0-1.5 s | 1.5-2.9 s |
| Java | 13.1 /s | 15.6 /s | 0.4-1.1 s | 1.0-1.5 s |

1 vCPU raises the ceiling for compile-heavy languages (C++ by 76%), lowers
it for short jobs (creating the next sandbox needs the second core), and
makes every single run 1.5 to 3 times slower. The default stays at 2 vCPU,
because below the ceiling it is the run time people feel. If C or C++ volume
is what you run out of, set gcc to `"cpu": "1"` and double its
`max_instances`.

If the caps do not fit, a busy period lets the runners take the whole quota,
the API cannot start instances, and callers get Cloud Run's own page:
"500 Server Error ... Please try again in 30 seconds". The API log then says
"request was aborted after waiting too long for an available instance".

### What happens beyond capacity

Measured on 14 November 2026 with `tests/loadtest`: 20,000 different
programs in 2 minutes (167 per second, above the ceiling), about 5% built to
fail in a known way.

- All 20,000 finished with the verdict they should have. No request to the
  API failed.
- The backlog drained in 3 min 43 s. Median time from submit to result was
  9 s, 90th percentile 60 s, slowest 113 s.
- Languages with spare capacity were not held up by the busy ones: PHP,
  PostgreSQL, MySQL and Rust stayed under 5 s at the 99th percentile, while
  C and C++ (the most loaded) reached 110 s.

A queued submission that has waited 20 minutes is marked Internal Error with
"no runner capacity". A `wait=true` caller gets 503 "queue is full" after
about 30 s.

Reaching this took a 100,000-submission test that failed and four reruns.
The first version had caps that exceeded the quota, and queued through Cloud
Tasks, which sends 1 task per second from a cold queue: of 100,000
submissions, 92,172 finished correctly, 5,930 were still queued after 20
minutes, 1,680 were refused at submit and 218 were wrongly reported as
Internal Error. No submission ever received a wrong pass or fail verdict.

## Cost

**While running.** Request-based billing: an instance costs money only
while it handles a request. A run costs roughly its duration on the runner
(2 vCPU, 2 GiB) plus the same duration on the API or the worker (1 vCPU,
0.5 GiB, shared by all concurrent requests). The 20,000-submission test
above cost about $0.67 of Cloud Run time, estimated from Cloud Monitoring at
list prices.

**While idle.** No instances, so no compute. What is left:

| | Idle cost |
|---|---|
| Cloud Run, 12 services at zero instances | none |
| Pub/Sub topic and subscription with nothing queued | none |
| Image storage in Artifact Registry | 11 to 18 cents a month: 1.6 GB after a first deploy, 2.3 GB once three versions of each image are kept, at $0.10 per GB above the free 0.5 GB |
| Firestore: analytics counters, and submissions until they expire | a fraction of a cent: about 1.7 KB of counters per hour that has traffic (measured), stored without indexes |
| Secret Manager, one key | a few cents a month at most |
| Cloud Logging, 7-day bucket | none: nothing is logged without traffic |

Image sizes are measured; the prices are list prices on 14 November 2026,
so this is an estimate and not a bill. Setting `min_instances` or
`api_min_instances` above 0 is what turns idle cost on: those instances
never stop.

## Tests

```bash
(cd runner && go test ./...)                 # supervisor logic, sandbox pool
(cd api && go test ./...)                    # limits, verdicts, grouping, dispatch
JUDGE_URL=... JUDGE_KEY=... python3 tests/e2e.py       # every language and verdict, live
JUDGE_URL=... JUDGE_KEY=... python3 tests/burst.py 300 # spike
JUDGE_URL=... JUDGE_KEY=... go run tests/loadtest/main.go -n 20000 -duration 2m   # sustained load
```

`tests/loadtest` submits different programs at a steady rate, about 5% of
them built to fail in a known way, and passes only if every one gets exactly
the verdict it should. Check the quota before running it at a new rate.

`docs/render.js` regenerates the two animations in this README:
`how-it-works.gif` from `docs/how-it-works.html`, and `playground.gif` by
recording a session against a deployed judge.

`probe/` holds a debug shell that can be deployed on top of any runner image
(`probe/on.sh <family>`). It executes arbitrary commands, so it is
IAM-protected and should be deleted after use:
`gcloud run services delete judge-probe --region us-central1`.

## Known gaps

- Pub/Sub delivers at least once, so a queued group can occasionally run
  twice. Finished submissions are skipped, and a duplicate that overlaps a
  run in progress produces the same result.
- The first SQL request after a long idle period takes 2-3 s: the engine
  that was pre-started in the idle sandbox has to be woken or restarted.
- A stored submission must fit a Firestore document (1 MiB). Larger outputs
  are returned in full to `wait=true` callers but stored truncated.
- Admin keys have no quota or rate limit. Temporary tokens have a
  submission cap but no rate limit.
- Instance caps are fixed per family, so a spike in one language cannot
  borrow capacity that another language is not using.
