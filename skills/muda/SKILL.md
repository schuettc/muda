---
name: muda
description: Use when starting or resuming delivery-waste optimization in a GitHub repository.
muda-version: ">=0.1.0"
muda-owned: muda
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

Measure before believing; verify in the real environment. Check any reviewer
or agent claim against its cited evidence. One finding/change per PR. A skipped
or “trusting…” path is a defect, never a pass. Record decisions only through the record CLI; never
hand-edit `.muda/`. Normal reads do not edit code or workflows;
agent-guided fixes need human approval and a PR. Skill installation is a separate,
explicitly requested write.

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

## Inspect and route

First, a pushed `muda/record-baseline-…` branch for which
`gh pr list --state all --head <branch>` returns nothing (no PR, open, closed
or merged) is muda-measure's handoff: switch to it and load muda-diagnose,
which reads the record there. More than one such branch: STOP and ask. It is
the only unmerged state routed.

If `.muda/` exists, run these local reads before resuming:

```sh
muda record check
muda record show --format json
```

Stop on invalid/newer schema; ask for review, never repair by hand. If either
reports `incomplete-baseline-set`, **STOP**: tell the human to re-run the exact
same `record baseline` command, or do what the reported problem says. Summarize
what the record and cited evidence establish: open findings and any fix
awaiting verification. Raise every exemption an exempt
finding still relies on whose `review-by` is past today (UTC); replaced ones
are history. A workflow
gate exemption covers the exact file; ones recorded before raw-file hashes must
be re-attested once. A gates record from before hashes (`policy:inventory:raw`)
is re-recorded with `record baseline … --gates`, never exempted.

If no `.muda/` exists:

**CHECKPOINT:** Your move: 'yes' to initialize a reviewed local record and start
measurement. Wait for the answer. Prepare muda-settings.json from confirmed information
only: one JSON object with `schema: 1`, `roles` (workflow-to-confirmed-role map),
`stacks` (confirmed stack list), `window` (`since` and `until` dates/RFC3339),
and `muda-version`: the exact first version token `muda version` prints
after muda (e.g. `dev`, `0.2.0`). `muda record init` happens
only after roles, stacks and window are confirmed; later settings change only
through a re-recorded baseline. If not yet confirmed, load muda-measure, which confirms
them, then runs init; this entry consent carries over. If already confirmed,
use only:

```sh
muda record init --file muda-settings.json
muda record check
```

Delete muda-settings.json only after `muda record init` and `muda record check` both succeed;
if either fails, keep it and **STOP**.

Choose the next phase from the validated record, not time pressure:

| State | Next action |
| --- | --- |
| No baseline, including after init | Load muda-measure. |
| Baseline, no findings yet, or open findings | Load muda-diagnose; no edits. |
| Approved finding | Load muda-fix. |
| `in-pr` finding | Load muda-verify. |
| `in-pr` finding whose receipt shows `UNVERIFIED` or `WEAKENED` | If the human accepts the policy proof gap, load muda-diagnose for an exact `gate:` exemption, then muda-verify. |
| `reopened` finding | Load muda-diagnose: revisit diagnosis and approval before another fix, including after a declined renewal. |
| Fixed finding | Inspect receipt/run evidence; no second fix for it. |
| Exempt finding | Inspect reason/agreed-by/review-by; if its review-by has passed, raise it and load muda-diagnose to renew or reopen. |
| All findings fixed or validly exempt, the recorded baseline window's `until` is later than every fixed finding's PR merge time (`gh pr view <pr> --json mergedAt`, compared as full timestamps), and no exemption `date` is after `until`'s UTC date (the same day counts as not later) | A new cycle's baseline is recorded, not yet diagnosed: load muda-diagnose. If a merge time can't be read, **STOP** and ask. |
| All findings fixed or validly exempt, otherwise | Stopping rule: report the program complete, citing each finding's receipt, then load muda-measure for the next cycle's baseline. |

For mixed states, report each and recommend which eligible finding to take
next. Before routing, check for an unmerged record PR
(`gh pr list --json headRefName`, filtered by the `muda/record` prefix); if one
exists, give the Your move merge ask: never route on
unmerged state. A fixed finding's PR with null `mergedAt` is unmerged:
report its fix as pending. It blocks any new cycle, even an explicit re-measure
(STOP and explain why). If that PR is open, load muda-verify to report its real
pipeline, recording nothing; if closed, **STOP** and ask. If the human
confirms it did not merge, offer `reopened` with a `reopen-reason` through
muda-diagnose; a merged fix needs a new finding. If the human
explicitly asks to re-measure, load muda-measure. Before routing an `approved` finding to muda-fix, check for an
open PR for it; if one exists, route to muda-verify. Record changes go through
PRs, never pushed directly to the default branch.

Open every record PR with a `--label` per muda.toml `record-pr` label and watch
its checks; never bypass or weaken one. On a repo-convention failure (missing
label, title format), propose the PR-carried fix, ask once, apply it, and record
a label on its branch with
`muda record baseline --file .muda/baseline.json --settings muda-settings.json`
(current settings plus `record-pr`).

If a routed skill is unavailable, report that phase as pending;
never invent its instructions. The entry checkpoint authorizes only init;
approval, status, exemptions and receipts belong to their reviewed phase.
Report uncertainties.
