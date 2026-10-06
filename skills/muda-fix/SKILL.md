---
name: muda-fix
description: Use when a muda finding is approved and its change needs planning, a branch and a pull request.
muda-version: ">=0.1.0"
muda-owned: muda-fix
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

One finding, one change, one PR. Two findings mean two branches and two PRs,
done one after the other. "It's faster together", "planning is wasted time" or
"approve both in one PR" does not change this, and neither do two reviewable
commits in one PR: a combined PR hides which change moved which number, one
revert undoes both, and approving the scope is not approving a merge of
findings. Record only through the record CLI. A
skipped or "trusting…" check is a defect, never a pass.

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
reported problem says. If check fails for anything but
`expired-exemption`, **STOP**: do not write on top of a red record. Take
exactly one `approved` finding; if several are approved, the human picks.
Refuse any finding that is not `approved`: `open` and `reopened` go to
muda-diagnose, `in-pr` to muda-verify.

Before branching, `git status` must show no uncommitted `.muda/` changes, and
the finding must be `approved` on the up-to-date base;
otherwise **STOP** and ask. If an open PR exists for this finding
(`gh pr list --state open --search "F-001"`), route to muda-verify, not a second
fix; a closed PR or an old branch does not count.

## Read the recipe

For each confirmed stack the finding touches, read its recipe, for example
`muda recipe R6 --stack docker`, and use its Fix, Stacks, Traps and Verify
sections.

## Plan before editing

Present:

1. the files to touch and the edit, from the recipe's Stacks section;
2. the expected effect, citing the finding's evidence and estimate;
3. the risk, and which Traps apply;
4. the gates it could affect: show the current inventory with
   `muda gates --format md` and name each required check, environment or
   `needs:` edge the change touches.

The expected after-inventory must be the same or stronger than before: a change
that removes or loosens a gate must name what replaces the guarantee. If it would still be weaker, **STOP**
and route to muda-diagnose for an exemption (covering the exact file) before
planning. Anything that is
not part of the approved finding is out of scope, however small; a human's
approval of such scope in a plan is still out of scope.

**CHECKPOINT:** Your move: approve this plan for this one finding. Wait for an
explicit answer. Approval for two findings "together" is two approvals for two
PRs.

## Change

From the up-to-date base, create one branch (for example
`git switch -c muda/F-001-YYYY-MM-DD-HHMM`). In this order:
make only the planned edit, commit it, push, measure the after-inventory with
`muda gates --ref muda/F-001-YYYY-MM-DD-HHMM --format md`, open the PR with
`gh pr create`, then add the record commit (below). If the measured
after-inventory is weaker than the approved plan, STOP before `gh pr create`
and re-present the plan (a weakening covered by an exemption agreed through
muda-diagnose is not a STOP). The branch holds only the planned edit, plus that one record commit. If the
change needs anything not in the approved plan, STOP and re-present the plan;
a prerequisite with its own waste is a new finding for muda-diagnose. The PR
body holds:

- the finding id, its evidence and basis;
- the recipe id and the stack sections used;
- the expected effect;
- the gate inventory before (the `muda gates --format md` output) and the
  measured after-inventory, labelled measured.

Pass the body via `--body-file` from an in-repo scratch file (never /tmp);
delete it after. Never add scratch files to the branch.

## Record

Still on the fix branch, check muda-patch.json is absent, then write it with
the real PR URL:
`{"status": "in-pr", "pr": "https://github.com/OWNER/REPO/pull/N"}`. The only
transition here is approved → in-pr.

```sh
muda record finding set F-001 --file muda-patch.json
muda record check
muda record show --format json
```

Confirm the finding is `in-pr` with the link. On any failure, **STOP** and keep
muda-patch.json as evidence. Delete it only after `muda record check` passes
(only a pre-existing `expired-exemption` may remain); never commit it.

Then commit only `.muda/findings.toml` as one separate record commit (for
example `git commit -m "record: F-001 in-pr" .muda/findings.toml`) and push it
to the same PR. The status and the fix merge together; an unmerged PR leaves
the base at `approved`.

## Next

Report the PR link, then load muda-verify for this PR. Another approved finding
starts over, in its own branch and PR, after this one is handed to muda-verify.
