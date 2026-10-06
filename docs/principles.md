# Principles behind the delivery standard

The general rules behind the cross-cutting decisions made while building this
standard. Each is one or two sentences with a one-line *why*. The
project-specific instance of each decision lives in the first adopter's own
records.

## Artifacts and identity

- **D1: Shared CI code is published once, versioned, and consumed by pin; each
  repo keeps only a thin caller and its own config. Dependabot moves the pins, and
  a fleet check fails on a stale pin or a vendored copy.**
  *Why:* one copy is tested once, updates arrive as reviewable one-line bumps, and
  no repo can drift because none carries the code.

- **D2: Every deployed workload references an immutable image identity recorded
  in infrastructure-as-code; mutable tags are never deployment inputs. Ad-hoc
  runs use an isolated location and per-run overrides.**
  *Why:* the running code is always recorded and rollback-able, and a local build
  can never silently change a scheduled or shared job.

- **D5: A layer cache needs a mutable store separate from the immutable artifact
  store, namespaced per consumer, with an expiry for superseded manifests.**
  *Why:* image repos must be immutable, but a cache tag is rewritten on every
  build, so the two cannot share a store.

## Gates and correctness

- **D3: CI tests the interpreter and the dependency set the artifact ships; one
  version file per repo is the source of truth, and installs come from the lock
  everywhere.**
  *Why:* a green gate then means "this interpreter and these libraries work",
  not "some nearby versions work".

- **D6: Every pin needs an owned refresh path that merges itself when the gate is
  green; a pin with no refresh path is W2 in another form. Auto-merge is only as
  safe as the gate, so D3 comes first.**
  *Why:* the real failure is green update PRs that nobody merges; a self-merging
  path on a trustworthy gate fixes it.

## Retiring risk

- **D4: Tooling that encodes a dangerous pattern and is unused gets retired, not
  kept "just in case"; check usage evidence (audit logs) before deciding.**
  *Why:* it matches actual use and the dangerous path disappears instead of
  waiting to be triggered.

## How the standard itself ships

- **D7: Shared CI lives in one public repo of versioned composite actions and
  reusable workflows, tagged vX.Y.Z; consumers reference an exact tag.**
  *Why:* a public repo can be called from any owner or org, and an exact tag is
  both the version record and the drift check.

- **D8: Publish code, keep config local.** Scripts ship inside the action at the
  tag; everything per-repo (inputs, schedules, runners, config files, Dependabot)
  stays in the consumer and is validated by the code at runtime.
  *Why:* the code is tested once at the source, and a consumer's diff is only
  what is truly theirs.

- **D9: When a second instance of a pattern appears, unify it now rather than
  recording it for later.**
  *Why:* one pattern per family, instead of two that drift apart.

- **D10: Tools run in CI from released, pinned, checksummed artifacts, never from
  a source repo.**
  *Why:* where the source lives and its visibility never matter to consumers, and
  CI never depends on a personal or private repo.

- **D11: A tool reads fleet data from wherever the fleet owner keeps it; the tool
  never owns another project's data.**
  *Why:* it gives downstream readers a stable interface, and fleet data stays
  consumer config.
