# Contributing

Thanks for taking the time. This is a small project and I read everything
that comes in, so a rough issue is better than no issue.

## Before you start

For a bug, a question or a small fix, go ahead and open an issue or a pull
request. For anything bigger, such as a new feature or a change to how
limits and verdicts work, please open an issue first. That way we can
agree on the shape of it before you spend an evening on the code.

Security problems are the one exception. Please don't report those in
public; [SECURITY.md](SECURITY.md) explains what to do instead.

## Finding your way around

| Folder | What's in it |
|---|---|
| `api/` | The public API and the queue worker, which are one Go program, and the playground under `api/web/` |
| `runner/` | The Go program inside every runner image: the pool of sandboxes and the supervisor that enforces the limits |
| `languages/` | One folder per runner service, each with a Dockerfile, a `service.json` and a manifest per language |
| `terraform/`, `deploy.sh` | The two ways to deploy. They read the same files. |
| `tests/` | The live suite, the burst test and the load test |
| `docs/` | Everything the README links to |

If you want the bigger picture first,
[docs/architecture.md](docs/architecture.md) explains how the pieces talk
to each other and why they're split the way they are.

## Running the tests

The unit tests cover the limits, the verdicts and the sandbox pool, and
they need nothing deployed:

```bash
(cd runner && go test ./...)
(cd api && go test ./...)
```

A real sandbox only exists on Cloud Run, so anything that touches a
runner, a Dockerfile or a language manifest also needs the live suite. It
runs every language and every verdict against a deployed copy:

```bash
JUDGE_URL=... JUDGE_KEY=... python3 tests/e2e.py
```

[docs/deploying.md](docs/deploying.md) shows how to deploy a copy of your
own. The `name_prefix` setting lets it sit next to an existing one, and a
copy that nobody is using costs cents a month. If you can't deploy one,
say so in the pull request and I'll run the suite myself.

## Adding a language or a package

This is the change I expect most often, so it has its own sections in the
deploying guide: [adding packages](docs/deploying.md#adding-packages) and
[adding a language](docs/deploying.md#adding-a-language). The short
version is that a language is a JSON file that says how to compile and run
it, sitting in a folder with a Dockerfile. A language that's rarely asked
for can share a folder, and so a service, with others.

When you add one, please also add a case for it to `tests/e2e.py` and a
sample to `api/web/samples.js`, so that it shows up when someone presses
"Run all" in the playground. The shared images also compile and run one
small program per language while they're being built (`selftest.sh` in the
folder, or a step in its Dockerfile), which catches a broken toolchain
before it's ever deployed, so add a line there too.

## What I look for in a pull request

- **One change at a time.** A bug fix and a refactor are easier to review,
  and to revert, as two pull requests.
- **A test for anything that changes behaviour.** A unit test where the
  logic lives in Go, and a case in `tests/e2e.py` where it depends on the
  sandbox.
- **Numbers that say where they came from.** The docs separate what was
  measured from what was estimated, and I'd like to keep it that way. If
  you add a timing or a cost, say how you got it.
- **Docs in the same pull request.** If the behaviour changes, the page
  that describes it should change with it.
- **Clean formatting.** `gofmt` and `go vet` for Go, `terraform fmt` for
  Terraform.
- **No quiet changes to the sandbox defaults.** No network unless it's
  asked for, no credentials and one job per sandbox are the reason this is
  safe to run.
  A change to any of them needs an issue and a discussion first.

## Licence

By contributing, you agree that your work is released under the
[MIT licence](LICENSE) that covers the rest of the project.
