---
name: muda-measure
description: Use when a repository needs a delivery-pipeline baseline, or its roles, stacks, gate inventory or window are unconfirmed.
muda-version: ">=0.1.0"
muda-owned: muda-measure
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

Measure before believing: teammate or chat claims are attributed claims to
check. A missing permission, gate or datum is `unavailable` with its reason,
never a pass or an empty inventory. Record only through the record CLI; never
hand-edit `.muda/` or the evidence files. Deadlines and sunk time never waive
the checkpoint. Small samples are normal: label `confidence`, never block on
them; muda-diagnose rules what they prove.

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

## Collect (read-only)

If `.muda/` exists, first run `muda record show --format json`; if it fails,
**STOP** and report; do not measure. If it or `muda record check` reports
`incomplete-baseline-set`, **STOP**: tell the human to re-run the exact same
`record baseline` command, or do what the reported problem says. Before the
first baseline, use muda.toml's window `since` for `30d`; a next cycle takes a
new one. Scratch files muda-settings.json, muda-gates.json, muda-scan.json and
muda-measure.json go in the repository root; if any exists, stop and ask.

**Replaying a closed window:** `--since` is the window's absolute start, never a
relative `30d`; measure, scan and notices take `--until` (the window's end).
Read gates and scan at the window's last commit (`muda gates --ref COMMIT`,
`muda scan --ref COMMIT`; `--ref` takes the full SHA) and add `--ref COMMIT`
to `record baseline`. Repository settings have no history: label them current,
not historical.

```sh
muda measure --since 30d > muda-measure.json
muda gates > muda-gates.json
muda scan --since 30d > muda-scan.json
muda notices --since 30d --format md
```

Check each exit status: a failed command's file is never evidence. If one fails
on GitHub's 1000-run listing cap, rerun all three with the same `--since`,
narrowed, and say from what to what. Report that window, never the full period.

## Present before any write

1. **Roles:** each workflow's candidate `role` and `role_reason`, keyed by
   workflow `path`.
2. **Stacks:** only from `github-actions`, `cdk`, `docker`, `python`, `node`, `go`;
   anything else is unrecorded.
3. **Window:** `since`, `until`, runs and `confidence` per workflow, outages,
   freezes, and each `definition_history` change (first run, runs before and
   after, jobs added or removed); an `unavailable` history cannot rule one out;
   `branch_only_versions` are not changes, and `approximate` means read at a
   pull request's head. Offer: start the window after the change (`--since` its
   `created_at`) and say the sample is small; keep the full window and say it
   mixes pipelines; or wait for more runs.
4. **Gate inventory:** every merge or deploy check (required checks, rulesets,
   environment protections, `needs:` edges) and each `unavailable` entry, with
   why.
5. **Where the time goes:** ranked sinks, signals and notices, cited; each cited
   number carries its `confidence` label.
6. **Existing record:** whether roles, stacks and window (both `since` and
   `until`) match muda.toml.

**CHECKPOINT:** Your move: confirm roles, stacks, gate inventory with its
unavailable gaps, and window as representative. Wait for an explicit answer on
each.

Record the gate inventory exactly as derived;
never trim, add or "green" it. Keep a human's dispute of a derived gate
attributed to them; settle it by an exemption (covering the exact file) or a
required-check change at source. If the window changes, rerun all three with
the same confirmed `--since` and reconfirm. If anything above cannot be
confirmed, stop without a baseline, saying what remains and what settles it.

If confirmed settings differ from muda.toml, ask the human to confirm the
corrected settings and re-record: `--settings` replaces it. Mid-cycle, if any
finding is `in-pr`, or `approved` with an open PR, **STOP** and ask:
re-baselining would move its verify window.

## Record (only after the checkpoint)

Write muda-settings.json: `schema: 1`, confirmed `roles` keyed by workflow `path` and known `stacks`, `window` with muda-measure.json's
`since` and `until`, and `muda-version` (the preflight's version token), plus any
`record-pr`. The
entry consent carries over. Run init only when `.muda/` is absent; always pass
`--settings`, `--gates` and `--scan` together.

```sh
muda record init --file muda-settings.json
muda record baseline --file muda-measure.json --settings muda-settings.json --gates muda-gates.json --scan muda-scan.json
muda record check
```

If any record command fails, **STOP** and keep the scratch files as evidence.
Delete the scratch files only after `muda record check` passes; never commit
them.

From the up-to-date base, create a record-only branch
(`git switch -c muda/record-baseline-YYYY-MM-DD-HHMM`), commit only `.muda/`
and push; open no PR: muda-diagnose adds findings and opens one record PR for
both. Exception (a measure-only run, or a next-cycle baseline diagnosed later):
open a record-only PR with `gh pr create` (entry skill's record-PR rule). Pass
the body via `--body-file` from an in-repo scratch file (never /tmp); delete it
after.

## Next

Report what was recorded and unavailable or unknown gaps, then load
muda-diagnose on this branch. In the exception, give the Your move merge ask.

## Next cycle

Run this skill again when muda-verify reports every finding `fixed` or validly
`exempt`: a new window, the same checkpoint and record command.
