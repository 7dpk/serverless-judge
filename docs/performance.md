# Performance and capacity

Everything here was measured on 14 November 2026 in `us-central1` with
2 vCPU runners, unless it says otherwise.

## How long a run takes

Time inside the runner for a small program, compile included.

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
Firestore write. Cold start is a new instance (scaling from zero, or
scaling out), measured as time to first byte from a client outside the
region, so it includes about 0.35 s of network. Blank cells were not
measured.

### The shared services

The 25 languages added later, measured on 15 and 16 November 2026 with the
same small program (read two numbers, print their sum). "In the sandbox" is
compile plus run, which is what a caller waits for.

| | Reported time | In the sandbox |
|---|---|---|
| Bash, Perl, Lua, SQLite, Plain Text | 10-40 ms | 15-70 ms |
| Ruby | 0.16-0.44 s | 0.4-0.9 s |
| R | 0.4-0.9 s | 0.8-1.3 s |
| Fortran, Pascal, COBOL, C with Clang | under 20 ms | 0.3-1.0 s |
| OCaml | 10 ms | 0.8-1.1 s |
| Erlang | 0-0.4 s | 0.7-1.1 s |
| Clojure | 0-0.9 s | 0.9-1.7 s |
| Elixir | 0-0.5 s | 0.9-2.4 s |
| Dart | 10 ms | 3.0 s |
| Groovy | 0-0.6 s | 2.5-5.0 s |
| Objective-C, D, C++ with Clang | 10-220 ms | 3.8-5.6 s |
| Haskell | 10-20 ms | 5.0-6.1 s |
| Kotlin | 0.1 s | 5.2-6.3 s |
| Scala | 0.5-1.0 s | 3.6-6.9 s |
| Swift | 10 ms | 1.6-7.5 s |

The long ones are compilers: Kotlin, Scala, Haskell and Swift take seconds
to build anything, and the first build on an instance that has just
started takes longest. Compiling doesn't count against the program's time
limit. Erlang, Clojure, Elixir and Groovy read low because their start-up
allowance has been taken off (see
[Differences from Judge0](api.md#differences-from-judge0)).

Bursts of different programs, starting cold, with the default instance
caps: 300 Python in 10.7 s, 1,000 Python in 29 s, 200 C++ in 22 s. All
verdicts correct.

To trade idle cost for no cold starts, set `min_instances` in a family's
`service.json`.

## Capacity and the CPU quota

Cloud Run gives a project a fixed amount of CPU per region: **200 vCPU** by
default ("Total CPU allocation, per project per region" under IAM & Admin >
Quotas). Every service draws on it, and idle instances keep counting for up
to 15 minutes after their last request.

So the instance caps must fit inside the quota. Both deploy paths check
this and refuse caps that don't fit:

    runners (sum of max_instances x cpu) + API + worker + 6 for rollouts  <=  quota

The defaults use all 200: 90 runner instances at 2 vCPU (gcc 17, python 11,
java 11, node 9, dotnet 7, mysql 7, go 5, postgres 5, php 4, rust 4, and 2
each for script, jvm, swift, functional and native), 6 API instances and 8
worker instances.

The ceiling was measured before the five shared services existed, when
their 10 instances belonged to the first ten families: about **135
submissions per second** for a mix of those 12 languages. With today's
defaults those families have 80 instances, so expect about 120; that
figure is scaled from the measurement, not measured again.

For more, request a higher quota from Google, raise `max_instances` in
`languages/*/service.json`, and deploy with `cpu_quota = <new quota>`
(Terraform) or `QUOTA_VCPU=<new quota>` (`deploy.sh`). Caps cost nothing
while idle, so with quota to spare they can be generous.

### One language, many people

Caps are per service and don't lend to each other (see
[Known gaps](architecture.md#known-gaps)). What matters for a drive in one
language is runs per second, not head count. Python sustained 50 runs per
second on 12 instances (the table below), so about 4 per instance, and
1,000 Python submissions sent at once finished in 29 s on the same 12. So 2,000 people
who each press Run every 40 seconds are 50 runs per second: 12 instances
keep up, and a rush queues and drains.

The 4 per instance is for small programs and hasn't been tested beyond 12
instances. To give one language more for the day:

```bash
terraform apply -var 'max_instances={ python = 60, gcc = 4, java = 4, node = 4 }'
```

That changes the services' scaling settings and the registry and builds
nothing, so it should be quick (it hasn't been timed). The caps still have
to fit the quota, which is why others come down in this example.

If the caps don't fit, a busy period lets the runners take the whole quota,
the API can't start instances, and callers get Cloud Run's own page:
"500 Server Error ... Please try again in 30 seconds". The API log then
says "request was aborted after waiting too long for an available
instance".

## Runner size: 2 vCPU or 1

Less than 1 vCPU isn't possible: Cloud Run allows it only in the
first-generation environment, and sandboxes need the second. Above 1 it
must be a whole number. So the choice is 1 or 2, set per family as `cpu` in
`service.json`. Measured at the same total vCPU, so twice as many instances
at 1 vCPU:

| | 2 vCPU, sustained | 1 vCPU, sustained | One run at 2 vCPU | One run at 1 vCPU |
|---|---|---|---|---|
| Python | 50.1 /s | 33.1 /s | about 31 ms | 34-186 ms |
| C++ | 10.9 /s | 19.2 /s | 1.0-1.5 s | 1.5-2.9 s |
| Java | 13.1 /s | 15.6 /s | 0.4-1.1 s | 1.0-1.5 s |

1 vCPU raises the ceiling for compile-heavy languages (C++ by 76%), lowers
it for short jobs (creating the next sandbox needs the second core), and
makes every single run 1.5 to 3 times slower. The default stays at 2 vCPU,
because below the ceiling it's the run time people feel. If C or C++ volume
is what you run out of, set gcc to `"cpu": "1"` and double its
`max_instances`.

## What happens beyond capacity

Measured with `tests/loadtest`: 20,000 different programs in 2 minutes (167
per second, above the ceiling), about 5% built to fail in a known way.

- All 20,000 finished with the verdict they should have. No request to the
  API failed.
- The backlog drained in 3 min 43 s. Median time from submit to result was
  9 s, 90th percentile 60 s, slowest 113 s.
- Languages with spare capacity were not held up by the busy ones: PHP,
  PostgreSQL, MySQL and Rust stayed under 5 s at the 99th percentile, while
  C and C++ (the most loaded) reached 110 s.

A queued submission that has waited 20 minutes is marked Internal Error
with "no runner capacity". A `wait=true` caller gets 503 "queue is full"
after about 30 s.

Getting here took a 100,000-submission test that failed and four reruns.
The first version had caps that exceeded the quota, and queued through
Cloud Tasks, which sends 1 task per second from a cold queue: of 100,000
submissions, 92,172 finished correctly, 5,930 were still queued after 20
minutes, 1,680 were refused at submit and 218 were wrongly reported as
Internal Error. No submission ever received a wrong pass or fail verdict.
