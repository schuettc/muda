---
name: muda-verify
description: Use when a muda finding is in-pr and its effect and gate result need verifying, before and after merge.
muda-version: ">=0.1.0"
muda-owned: muda-verify
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

Verify in the real environment. Green PR status alone is never fixed. A
skipped or "trusting…" job is a defect, never a pass. Record only through the
record CLI.

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

```sh
muda record show --format json
muda record check
```

If the read fails, **STOP** and report; for `incomplete-baseline-set`, tell the
human to re-run the exact same `record baseline` command, or do what the
reported problem says. If check fails for anything but `expired-exemption`,
**STOP**: do not write on top of a red record. Take one `in-pr` finding with
its `pr`. Before merge the base shows `approved`: check out the fix
branch, run `muda record show --format json` there, and return to the
up-to-date base before Phase 2. Take `--before` from the baseline window; pass
`--since` as `Nd`, N the whole days in the baseline window (`until` − `since`, from `muda record show --format json`).

## Confirm jobs ran

List each compared run's jobs with `muda logs 1234`: any `skipped`
conclusion on a relevant job is a defect.
Search logs, e.g.
`muda logs 1234 --job 5678 --grep "(?i)skip|trusting"`.

In both phases, for every required check whose job got faster, compare what
it executed before and after (`--grep "(?i)cached|skip"`): tests run vs
replayed, packages built, steps run or skipped. Less work is a weakened gate,
even with an unchanged inventory and a faster time: report it, and fix it in
the same PR, or **STOP**.

## Phase 1: the PR's real pipeline

Take the PR's completed runs and confirm their jobs ran. Run Phase 1's
compare from the up-to-date base, at the PR branch:

```sh
muda compare --before 2026-09-01..2026-09-29 --after-runs 1111,2222 --gates --scan --ref muda/F-001-YYYY-MM-DD-HHMM --since 28d > muda-compare-pr.json
```

Any non-zero exit with an empty or missing report is an error;
exit 2 is usage. Before claiming an effect, check `before_unavailable`,
`after_unavailable` and deltas with `presence: "unavailable"`: a missing
before side is not an improvement. Report the measured effect against the
finding's estimate, and the gate and scan results (read as in Phase 2), before
the merge ask. A PR that edits a workflow reads `UNVERIFIED`
before merge (the file's hash changed). Only changed workflow files need an
exemption. If the human accepts a changed file, read its `workflow_diffs`
entry, then agree an exact `gate:` exemption for that hash through
muda-diagnose before merging; its `reason` cites that diff. Never merge on `WEAKENED`.

An experiment (a `suspected` finding) is judged by its `estimate`'s pass
criteria. Met: the `fixed` patch adds `"basis": "measured"`, citing the receipt.
Not met: the human closes the PR; from the base, patch `reopened` and the new
`evidence` (still `suspected`) through a record PR. Any other fix PR closed
unmerged leaves the base `approved`: record nothing; load the entry skill.

## Phase 2: after merge

Pull the base (now `in-pr`). Take post-merge runs on the
default branch, confirm their jobs ran, then compare:

```sh
muda compare --before 2026-09-01..2026-09-29 --after-runs 3333,4444 --gates --scan --since 28d > muda-compare-merged.json
```

With `--gates`, compare exits 1 after its report when a gate is removed,
weakened or unverified without exemption. Present the `removed`, `loosened`,
`unverified` and `exempted` lists. The gate status
(`UNVERIFIED` outranks `WEAKENED`) is:

- `VERIFIED`: the same or stronger, proven;
- `EXEMPTED`: every problem has an exact active exemption for its `gate:`
  location (explicit acceptance, not proof; say so in the receipt);
- `WEAKENED`: a gate was removed or loosened;
- `UNVERIFIED`: proof is missing.

Never mark `fixed` on `UNVERIFIED` or `WEAKENED`.
A changed workflow file reports `UNVERIFIED` without an exact accepted
exemption; a byte-identical one (equal full SHA-256) is `VERIFIED`.
`VERIFIED` covers `.github/workflows` files only, not local composite actions
(`uses: ./…`), scripts a step runs or remote actions on moving refs. A gate
exemption covers the exact file as it is now; any later edit needs a new
exemption. A removed workflow is exempted by the exact file removed.
Exemptions recorded before raw-file hashes must be re-attested once; a gates
record from before hashes (`policy:inventory:raw`) is re-recorded with
`record baseline … --gates`, never exempted.

Present the `removed` and `added` signal lists, never netted: signals match by
signal and summary, so a change within one file reads as removed plus added;
an `added` signal is grounds to review the finding with the human. A removed signal is not proof of a fix unless the
finding's cited location changed as the recipe says: a removed
`uncached-install` after a job rename is a rename, not a cache fix; ask the
human. A scan comparison `unavailable` means **STOP** and ask the human, never
"nothing added" or "removed".

**CHECKPOINT:** Present the measured effect (before, after, change, runs
cited), the jobs confirmed run and their work, gate lists and status, and
signal lists.
Your move: confirm them and decide `fixed`, `reopened`, or that it stays `in-pr`
with a receipt saying why. Wait for an explicit answer. Time pressure changes none
of this.

## Record (only after the checkpoint)

One scratch file at a time: check it is absent, write it, run its command and
`muda record check`; delete it only after `muda record check` passes (only a
pre-existing `expired-exemption` may remain). On any failure, **STOP** and keep
it as evidence; never commit it.

muda-receipt.json is one object: `pr` (the finding's PR URL), `before` and
`after` (Markdown from the merged compare), `run-links` (each an
`https://github.com/OWNER/REPO/actions/runs/ID` URL, covering both the PR and
the post-merge run sets), `gate-status` (exactly the compare status) and
`gate-result` (the gate lists, signals removed and added, and any exemption
ids). `before`, `after` and `gate-result` are strings.

```sh
muda record receipt F-001 --file muda-receipt.json
muda record check
```

Then write muda-patch.json: `{"status": "fixed"}` (`pr` is kept)
or `{"status": "reopened"}`. For `in-pr` with a receipt, skip the patch.

```sh
muda record finding set F-001 --file muda-patch.json
muda record show --format json
```

Delete the compare files. From the
up-to-date base, create a record-only branch
(`git switch -c muda/record-verify-F-001-YYYY-MM-DD-HHMM`), commit only
`.muda/`, push, and open a record-only PR with `gh pr create` (entry skill's record-PR rule). Pass the body via `--body-file` from an in-repo scratch file
(never /tmp); delete it after.

## Next

After the Your move merge ask, once merged:

- If the only blocker is `UNVERIFIED` or `WEAKENED` policy proof the human
  accepts, load muda-diagnose to agree an exact `gate:`
  exemption. The finding stays `in-pr`; re-run muda-verify after that
  exemption's record PR is merged.
- If every finding is `fixed` or validly `exempt`, report the program complete,
  citing each finding's receipt and one read-only program-level comparison
  (baseline window vs last merge to now; not recorded), then load
  muda-measure for the next cycle's baseline:

  ```sh
  muda compare --before 2026-09-01..2026-09-29 --after 2026-10-01..2026-10-15 --gates --format md
  ```

- Otherwise load the entry skill.
