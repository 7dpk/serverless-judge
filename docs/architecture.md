# Architecture

```
                      wait=true
client ─X-Auth-Token─> judge-api ───────────────────────────┐
                         │ wait=false                       ▼
                         └─> Pub/Sub ─push─> judge-worker ─> runner-<family> ─ sandbox ─ judge box ─ jexec ─ program
                Firestore: submissions, language registry
```

## The parts

- **judge-api** (`api/`, `ROLE=api`) is public. It validates submissions,
  stores them in Firestore, and decides the verdict by comparing output on
  its side. Expected output never enters a sandbox. It runs `wait=true`
  submissions itself and publishes the rest to Pub/Sub.
- **judge-worker** (same image, `ROLE=worker`) is private. Pub/Sub pushes
  each queued group to it and it runs them. It's a separate service so a
  backlog of queued work can't use up the capacity that answers API
  callers.
- **runner-\<family\>** (`languages/<family>/`) is one Cloud Run service per
  toolchain image, concurrency 1, with a service account that has no roles.
  Only the API's identity may invoke it. The busy languages have an image
  each; the rest share five (`script`, `jvm`, `swift`, `functional`,
  `native`), so they don't each hold a slice of the CPU quota.
- **judge** (`runner/`) is one static binary in every image. `judge serve`
  is the HTTP server. For each job it starts `sandbox do -- judge box`, the
  supervisor that compiles, runs each test under limits and reports back.
  `jexec` is a tiny C launcher that applies rlimits and measures the
  program.

## How a run goes

- One sandbox serves exactly one job and is then destroyed. The runner
  creates the next sandbox while the current job runs, so a job normally
  waits 0 ms for one.
- A batch of the same program with different inputs is compiled once and
  run as several tests in a single sandbox.
- When there is more work than runners, it waits in order at a per-language
  gate inside the API or worker, as wide as that language's instance cap.
  One busy language doesn't slow the others. `api/dispatch.go` explains the
  design and the two that were tried and measured before it.

## Safety

- The program runs in a gVisor sandbox inside a second-generation Cloud Run
  instance. By default the sandbox has no network, no metadata server, an
  empty environment and a read-only filesystem apart from in-memory `/tmp`.
- One job per sandbox; one request per instance at a time.
- The runner's service account has no roles, and runners accept calls only
  from judge-api.
- The supervisor shares the sandbox with the program, so it protects its
  report rather than relying on the program behaving: it is non-dumpable,
  the report is tagged with a nonce the program never sees, and a missing
  or untagged report is an Internal Error, never a pass.
- CPU, wall time, memory, output size, file size, process count and total
  filesystem growth are all capped.

### Network access

Off by default, and then none of this applies: no sandbox has a network.
A deployment can allow it (`allow_network`), and a submission then has to
ask (`enable_network`) with a key that may. See
[the API page](api.md#network-access) for the limits; this is how they
hold.

- **A sandbox of its own.** Network access is decided when a sandbox is
  created. A run that asks gets a new sandbox made with it; the sandbox
  kept waiting for the next job never has it, and no ordinary run can end
  up in one that does.
- **Two switches.** The API refuses to pass the request on unless the
  deployment allows it, and each runner refuses it again unless it was
  itself deployed with network access allowed.
- **What it can reach** (measured from inside such a sandbox): public
  hosts, over IPv4, on any TCP port except 25. The metadata server answers
  DNS and nothing else, so there are still no credentials to fetch. The
  runner hosting the sandbox isn't reachable, and the runner services have
  no VPC attached.
- **Counted twice.** The supervisor reads the sandbox's network counter
  every few milliseconds: past the download limit it stops the run, and
  what has arrived so far sets the CPU credit. The runner, outside the
  sandbox, reads the instance's counter as well. A program can stop or
  kill the supervisor it shares a sandbox with; it can't reach the runner.
  In a test a program froze the supervisor and went on downloading: the
  runner destroyed the sandbox at the limit and the submission ended as
  Internal Error, 4.5 s after it started.
- **Time.** The wall limit grows by the network time allowed; the CPU limit
  only by the credit for bytes received. A program that hangs on a
  connection ends as Time Limit Exceeded when the wall limit is up.

What it doesn't do: filter where a run may connect. Cloud Run has no such
control for a sandbox, and the ones that exist for a service (a NAT
gateway, a proxy) have an hourly price, which would end "costs nothing
while idle". So a caller who may ask for network access can make the
judge talk to any public host for up to about 70 seconds per run. Grant it
accordingly.

Cloud Run sandboxes are in **public preview**: no SLA, and behaviour may
change. If that becomes a problem the fallback is the first-generation
environment with the supervisor run directly;
`runner/internal/serve/pool.go` is the only place that knows about the
sandbox CLI.

## Data retention

- **Submissions** (code, input, output, verdict) expire 6 days after they
  are created and are deleted by Firestore within about a day of that, so
  nothing is stored beyond 7 days. An expired submission answers 404
  straight away. Set `SUBMISSION_TTL_HOURS` on judge-api (or
  `submission_ttl_hours` in Terraform) to keep less.
- **Analytics** are kept indefinitely. They are counters per hour, per day
  and per language: how many finished, with which verdicts, a latency
  histogram, and totals for run time and memory. Nothing in them identifies
  a submission, which is why the dashboard still works after the
  submissions are gone.
- **Temporary tokens** are deleted when they expire.
- **Logs** of the judge's services go to their own Cloud Logging bucket,
  `judge-logs`, kept for 7 days, and are excluded from the project's
  default 30-day bucket. They hold request lines and timings, never code or
  output. A new routing rule took up to 4 minutes to be applied when
  measured, so a first deploy creates the services 5 minutes after the
  rule. To read them:
  `gcloud logging read '<filter>' --bucket judge-logs --location global --view _AllLogs`.
- **Queued messages** in Pub/Sub are dropped after 30 minutes.

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
- Instance caps are fixed per family, so a spike in one language can't
  borrow capacity that another language isn't using. For a drive in one
  language, raise that family's cap for the day
  (`max_instances = { python = 60 }`), or ask Google for a larger quota and
  raise them all: see
  [Capacity](performance.md#capacity-and-the-cpu-quota).
- Some first HTTPS connections from a sandbox with network access take
  about 3.6 s to complete their handshake (seen to S3, npm and Cloudflare,
  not to Google, GitHub or PyPI). The cause wasn't found; it is one reason
  the default network time is as long as 30 s.
