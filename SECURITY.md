# Security

This project exists to run code written by strangers, so a hole in it
matters more than it would in most projects. If you've found one, I'd much
rather hear about it early, and I'm grateful that you looked.

## Reporting a vulnerability

Please don't open a public issue. Use GitHub's private reporting instead:
open the
[Security tab](https://github.com/7dpk/serverless-judge/security/advisories/new)
and choose "Report a vulnerability". Only you and I can see what you send.

It helps a lot if the report includes:

- what you were able to do that you shouldn't have been able to;
- the submission or request that does it: the language, the source code,
  the input and the limits;
- how your copy was deployed (Terraform or `deploy.sh`, the region, and
  anything changed from the defaults) and which commit it was built from.

I maintain this on my own, so I can't promise a fixed response time. I do
read every report, I'll reply as soon as I can, and I'll tell you what I
found and what I'm going to do about it. I'm happy to credit you in the
fix, unless you'd rather not be named.

## What I'd like to hear about

- A program that reaches the network, the metadata server or any
  credentials when it shouldn't be able to.
- A program that sees another submission's code, input or output, or
  changes the verdict of a later one.
- A program that outlives its sandbox.
- A way around the CPU, memory, time, output or disk limits that lets one
  submission starve the others.
- A way to read or change submissions, tokens or settings without the
  right key.
- A temporary token that can do anything beyond submitting and reading
  results.

## What is out of scope

- **Bugs in gVisor or Cloud Run themselves.** Those belong to Google, and
  the place to report them is <https://bughunters.google.com>. I'd still
  appreciate a note, so I can add a mitigation on this side.
- **Things an admin key can do.** The admin key is trusted by design. It
  has no rate limit, and the README says so.
- **Settings of a particular deployment**, such as someone publishing
  their own key.

## Supported versions

There are no releases yet, so fixes go to the latest commit on `main`.

## Please test on your own copy

A copy of your own takes about 11 minutes to deploy and costs cents while
it sits idle, so there's no need to test against anyone else's. The
[deploying guide](docs/deploying.md) has the steps.
