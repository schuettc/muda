---
id: R1
title: Pin the toolchain through version files
waste: [W2]
signals: [floating-ref, floating-runner, floating-toolchain]
stacks: [github-actions, docker, python, node]
---

## Symptom

The same tree produces different templates or dependencies on different runners;
CI tests different interpreter patches from the deployed artifact.

## Detect

```sh
muda nonexistent
muda scan
```

Follow floating refs, runners and toolchain signals to their files. Read setup
and build logs for resolved versions; compare CI, deploy, release and local
version sources. A declared pin is not proof that the requested version ran.

## Fix

Agree one exact version source per interpreter with the user, make every reader
consume it, and pin actions, runners and base images to reviewed identities.
Give every pin an owned refresh path. Present the plan for approval and ship a PR.

## Stacks

### github-actions

Before: `node-version: 24` and `runs-on: ubuntu-latest`. After: use
`node-version-file: .nvmrc` containing an exact supported patch and an explicit
runner OS label. Replace floating action refs with reviewed commit identities;
ensure update automation can refresh them.

### docker

Before: `FROM python:3` or `FROM node:24`. After: select an exact compatible
base image by digest and assert its interpreter matches the version source.
Build smoke checks against the actual shipped image, not a nearby host runtime.

### python

Before: workflows each choose a Python major/minor. After: an exact
`.python-version` is read by setup, test, packaging and release jobs; install
from the frozen lock, without resolving a new dependency set.

### node

Before: inline majors differ between test and deploy. After: `.nvmrc` carries
an exact supported patch and every setup reads it; use the package lock for
reproducible installs in CI and the image.

## Traps

- A self-hosted runner may bake a different runtime. Inspect the first setup log
  to prove the exact requested patch was fetched; reject silent use of the baked one.
- A local installer may not support a new patch on every OS. Make overrides
  explicit and prove the target runtime in the reference environment.
- CI green is not proof that the deployed interpreter and dependency set were
  tested (lesson 4). Inspect what installs and runs in the actual artifact.
- Pins without an owned, gated refresh path merely defer drift.

## Verify

```sh
muda compare --before 2026-09-01..2026-09-08 --after 2026-09-08..2026-09-15 --gates
muda record check
```

The dates above are example windows; replace them with representative before/after windows.
Confirm resolved versions match across CI, image, deploy and release. Compare
repeat synthesis of the same tree and retain all existing tests and smoke gates.
Verify on the real PR pipeline and again after merge. The gate inventory must
remain the same or stronger; the user confirms the receipt.

## Evidence

Illustrative example only, not a measured adopter result: 2 differing runtime resolutions become 1 exact resolution, with repeat
synthesis producing identical templates.
Record actual before/after measurements, coverage, tree/asset identities and
run/job/step sources in the finding receipt; never substitute these example numbers.
