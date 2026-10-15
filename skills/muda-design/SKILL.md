---
name: muda-design
description: Use when a pipeline or deploy change is being designed or planned (a new image, stack or workflow, or splitting a service) and should be checked against muda's recipes before code is written.
muda-version: ">=0.1.0"
muda-owned: muda-design
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

Use this skill on request only; nothing suggests it on its own. It reads the
design and the repository, nothing more. It never writes `.muda/` (nothing is
measured, so there are no findings), edits no code, opens no PR and needs no
recorded baseline. Never recommend anything that weakens a gate or check: the
gate wins, as the standard says.

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

## Read the design

Take whatever the person brings: a design doc, a plan or a description in
conversation. Read the repository as it is now to learn which stacks the
design touches: `github-actions`, `cdk`, `docker`, `python`, `node` or `go`. It works the
same on a change already written: read the design and its code.

## Check it against the recipes

1. List the recipes for each stack the design touches, e.g.
   `muda recipes --stack cdk` and `muda recipes --stack docker`.
2. Read each one for that stack (`muda recipe R13 --stack docker`, …) and
   check the design against its Symptom, its Fix, the stack's "After" shape
   and its Traps. Do not run Detect sections: they read runs and logs, and no
   runs or logs exist yet.
3. Mark each recipe `applies` or `does not apply`, with why. A recipe applies
   only when the design has the shape its Symptom describes; never stretch a
   recipe to fit, and never file a risk under the nearest one.

**CHECKPOINT:** One line per recommendation: what to change in the design,
the recipe id, and why it applies here, each labelled **expected, not
measured**. Every recipe considered, as `applies` or `does not apply` with
why, goes to the uncommitted scratch report `.muda-design.md`. If no recipe
applies, say so plainly. Your move: reply 'yes' to take these into the design,
or name changes. Next: build the change.

Nothing is recorded. Once the change is built and running, muda-measure can
measure it; until then every saving stays expected.
