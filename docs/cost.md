# Cost

## While it's running

Billing is request-based: an instance costs money only while it handles a
request. A run costs roughly its duration on the runner (2 vCPU, 2 GiB)
plus the same duration on the API or the worker (1 vCPU, 0.5 GiB, shared by
all concurrent requests).

The 20,000-submission load test in [performance.md](performance.md) cost
about $0.67 of Cloud Run time, estimated from Cloud Monitoring at list
prices.

## While it's idle

No instances, so no compute. What's left:

| | Idle cost |
|---|---|
| Cloud Run, 12 services at zero instances | none |
| Pub/Sub topic and subscription with nothing queued | none |
| Image storage in Artifact Registry | 11 to 18 cents a month: 1.6 GB after a first deploy, 2.3 GB once three versions of each image are kept, at $0.10 per GB above the free 0.5 GB |
| Firestore: analytics counters, and submissions until they expire | a fraction of a cent: about 1.7 KB of counters per hour that has traffic (measured), stored without indexes |
| Secret Manager, one key | a few cents a month at most |
| Cloud Logging, 7-day bucket | none: nothing is logged without traffic |

Image sizes are measured; the prices are list prices on 14 November 2026,
so this is an estimate and not a bill.

Setting `min_instances` or `api_min_instances` above 0 is what turns idle
cost on: those instances never stop.

## Image sizes

One image per toolchain and one shared by the API and the worker. Sizes are
compressed, which is what Artifact Registry stores and bills and what Cloud
Run pulls. Measured on the deployed images on 15 November 2026.

| Image | Runs | Size |
|---|---|---|
| `judge-api` | the API and the worker | 8 MB |
| `runner-php` | PHP 8.4 | 46 MB |
| `runner-postgres` | PostgreSQL 18 | 47 MB |
| `runner-python` | Python 3.13 | 72 MB |
| `runner-go` | Go 1.26 | 85 MB |
| `runner-node` | JavaScript and TypeScript on Node.js 24 | 96 MB |
| `runner-gcc` | C and C++ on GCC 14 | 152 MB |
| `runner-java` | Java on OpenJDK 25 | 159 MB |
| `runner-mysql` | MySQL 8.4 | 244 MB |
| `runner-rust` | Rust | 333 MB |
| `runner-dotnet` | C# on .NET 10 | 355 MB |

The eleven add up to 1.6 GB. Two build stages are stored beside them:
`judge-bin` (3 MB, the runner binary copied into every runner image) and
`postgres-prebuilt` (158 MB, PostgreSQL as compiled, from which
`runner-postgres` takes only what it needs).
