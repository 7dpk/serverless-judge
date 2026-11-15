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
  Only the API's identity may invoke it.
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
  borrow capacity that another language isn't using.
