---
name: muda-diagnose
description: Use when a recorded muda baseline needs evidence-backed findings, ordering or exemptions.
muda-version: ">=0.1.0"
muda-owned: muda-diagnose
---

## Preflight

Before inspecting or changing delivery workflows:

1. Run `muda version`; it prints muda and then the version. Require >=0.1.0, or a
   `dev` version only under the capability check below; tell the user a `dev`
   binary is an unversioned build. Run `muda commands --json` and confirm the
   delivery surface includes measure, scan, notices, gates, logs, compare,
   recipes, recipe, standard, record and skills. A binary missing any of them
   is incompatible. If missing,
   give the exact install command
   `curl -fsSL https://muda.tools/install.sh | sh`. If old or incompatible,
   give `muda update`. **STOP** until a compatible binary is available; do not
   invent a fallback or claim installation succeeded without observing it.
2. Confirm the working tree's origin is a GitHub repository without printing
   credential-bearing remote URLs. Run `muda gates --format md` to verify
   repository and token resolution with a small, bounded read. Replaying a
   closed window, add `--ref` with its last commit's full SHA. It resolves
   GH_TOKEN, then GITHUB_TOKEN, then optionally gh authentication; gh is not
   required when an environment token resolves. Never print credentials. A
   hard failure (the default branch is unreadable: bad repo or token) means
   **STOP**. `unavailable` entries (missing admin, protection or ruleset
   permission) are not a probe failure: note them for muda-measure. A
   successful probe is not a confirmed representative baseline or gate
   inventory.

## Rules

Measure before believing: a finding follows evidence, not a deadline. A
teammate's number or cause is an attributed claim, never a measured estimate.
A long step labelled Docker does not prove a rebuild. Record only through the
record CLI; never hand-edit `.muda/`.

**Messages:** recommend, never survey: decide every answer (order, labels,
approval). One line per checkpoint item: what, basis, key number,
recommendation; full evidence and every recipe outcome go to a named,
uncommitted scratch report (e.g. `.muda-diagnose.md`). At most three status
bullets; no open "go ahead?". End with one ask: "**Your move:** reply 'yes' or
name changes. Next: what follows." Ready PR: "say 'merge' and I'll merge #N
(link) once green, or merge and reply 'merged'".

**Merging:** only on an explicit yes to that PR, every required check green on
the reported head: `gh pr merge N --squash --match-head-commit SHA`. No admin
bypass, removed check, or gate-passing label unless a `record-pr` label or the
human agreed. A fix PR merges only after Phase 1 verify is shown.

## Read the record

Handed muda-measure's record branch, stay on it: read the record there.

```sh
muda record show --format json
muda record check
```

If the read fails, **STOP** and report; for `incomplete-baseline-set`, tell the
human to re-run the exact same `record baseline` command, or do what the
reported problem says. Take static signals from `.muda/scan.json` and
re-run `muda scan --since 28d --format md` (`--since` as `Nd`, N
the whole days in the baseline window) only to refresh them; with no `scan`,
use a fresh one and say so. If `muda record show` reports `historical`, every
`muda gates` and `muda scan` (this refresh, any recipe Detect) adds `--ref`
with `historical.ref`; scan, measure and notices add `--since` and `--until` from
`settings.window`, not `Nd`. A stored signal gone from a fresh scan is not a
fix: show both. Before any write, run `muda record check` and note
pre-existing problems. If it fails for
anything but `expired-exemption`, **STOP**: do not write on top of a red
record. That code is tolerated whether the renew or decline decision is made
now or deferred.

## Existing findings

`open` and `reopened` findings join new candidates at the checkpoint.
After a new baseline, re-check each carried-over `open`, `approved` or
`reopened` finding's evidence against the new baseline. For stale evidence,
re-run the `logs` or `measure` read for its cited run, job and step, then patch
the refreshed `evidence`. A covered location never gets a second `finding add`.
Raise every exemption an exempt finding relies on whose `review-by` is past
today (UTC).

## Diagnose, in order

Sinks first, then signals, singly:

1. **Recipes:** list candidates by signal and confirmed stack, e.g.
   `muda recipes --signal slow-step --stack docker`, then read every recipe it
   lists (`muda recipe R6 --stack docker`, …) and run each one's Detect section
   or note why it does not apply. One sink can carry several wastes.
2. **Evidence:** read the cited job log and code or IaC:

   ```sh
   muda logs 1234 --job 5678 --step "Build image" --grep "CACHED|exporting"
   ```

3. **Label:** `measured` when cited runs, log lines or files show the waste;
   `suspected` when plausible but unshown, naming the evidence that settles it;
   `not shown` when its Detect ran and the evidence does not show the waste. A
   Detect not run is never `not shown`. A finding needs a recipe: never file it
   under the nearest recipe, or under one whose Detect section does not confirm
   the waste; report it unmatched. On a low-`confidence` sample, `measured`
   needs same-run or paired evidence; label any extrapolated saving.
4. **Estimate** saving, risk and effort, each with its basis, or give no
   number.

**CHECKPOINT:** Present each candidate, existing finding and expired exemption.
For each sink or signal, the report lists every recipe step 1 listed, with one
outcome each: `measured`, `suspected` or `not shown`, with the evidence read;
or `does not apply`, with the reason. An empty outcome is not allowed, even
once another recipe explains the sink. Your move: agree your recommended
findings, order, approval, exemptions and renewals, each with a reason. Wait
for explicit answers. A `suspected` finding is approved only as an experiment: its `estimate` starts `Experiment:` and
states pass criteria. It stays `suspected` and never feeds a saving.

## Record (only after the checkpoint)

One scratch file at a time: check it is absent, write it, run its command and
`muda record check`; delete it only after `muda record check` passes (only a
pre-existing `expired-exemption` may remain), then write the next. On any
failure, **STOP** and keep it as evidence; never commit it.

In agreed order, muda-finding.json is one object: `waste` (W-number),
`location`, `recipe` (R-id), `evidence`, `basis`, `estimate` (saving and
effort) and `risk`. `estimate` and `risk` are strings.
Each evidence item has a `kind`, run/job/step/file/log/annotation (`file` for
code or IaC), and a `run_id`, `job_id`, `url` or `path`; optional `step`,
`line`, `note`. `run_id` and `job_id` are integers. A location is `gate:ID` or
`workflow:PATH[/job:JOB[/step:STEP]]`, never a numeric step index; encode `/`
and `%` inside names as %2F and %25.

```sh
muda record finding add --file muda-finding.json
muda record check
```

Status changes use muda-patch.json, e.g. `{"status": "approved"}`;
then confirm the status with `muda record show --format json`:

```sh
muda record finding set F-001 --file muda-patch.json
```

Transitions here: open/reopened → approved, or → exempt; in-pr and fixed belong
to muda-fix and muda-verify, except `fixed` → `reopened` when
`gh pr view <pr> --json mergedAt` is null, cited in `reopen-reason`.

For an agreed exemption, muda-exemption.json holds `id` (new E-…), `waste`,
`location` (the finding's exactly), `reason`, `agreed-by` (the human), `date`
(the agreement date) and `review-by` (on or after today, UTC), both YYYY-MM-DD.
Then patch `{"status": "exempt", "exemption-id": "E-…"}`:

```sh
muda record exempt --file muda-exemption.json
muda record finding set F-001 --file muda-patch.json
```

A gate exemption is `record exempt` alone: location `gate:ID` from the compare
report's `unverified`, `removed` or `loosened` IDs, not the finding's location,
same fields and `review-by` rule; no `finding set`.
It covers the exact file as it is now; any later edit needs a new exemption.
A removed workflow is exempted by the exact file removed. Exemptions recorded
before raw-file hashes must be re-attested once; a gates record from before
hashes (`policy:inventory:raw`) is re-recorded with `record baseline … --gates`,
never exempted.

An exemption raised or renewed because the kept path catches a bug class needs
the named test that catches that class, and reverting that bug's fix must turn
the proposed faster gate red, or it is not granted or renewed; record both in
`reason`.

Renewal: a new exemption, then that patch with its id; the superseded one
becomes history. Declined renewal: patch `{"status": "reopened"}`, legal only
after its `review-by` has passed.

On a handed branch, commit only `.muda/` there; else on a record-only branch
from the up-to-date base (`git switch -c muda/record-findings-YYYY-MM-DD-HHMM`).
Push, and open a record-only PR with `gh pr create` (entry skill's record-PR rule). Pass the body via `--body-file` from an in-repo scratch file (never
/tmp); delete it after. On a handed branch it is the one PR for baseline and
findings, even with none agreed.

## Next

Report what was recorded and what remains, with the Your move merge ask. The
next phase starts only once that
record PR is merged; then load muda-fix for one approved finding.

If no findings are agreed and every recorded finding is `fixed` or validly
`exempt`, report this cycle complete, citing the baseline; the
human decides when to measure again. Do not load muda-measure.
