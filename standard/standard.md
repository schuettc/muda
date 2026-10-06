# The delivery-waste standard

A pipeline should spend time only on work that needs doing. No gate gets weaker
in exchange for speed. Recipes [R1–R11](../recipes/) give stack-specific fixes;
this standard supplies the stopping rule, waste catalog and exemption policy.

## Principles

1. **The stopping rule is not a number.** Stop when every detected finding is
   fixed with evidence or explicitly exempt. A clean measurement becomes a
   regression baseline, not a target or a scorecard. Unassessed work is not fixed.
2. **The gate wins.** Confirm the merge and deployment gate inventory with the
   user before changing anything. Verification must show the same or stronger
   gates; retain justified work rather than silently deleting it to buy speed.
3. **No silent fallbacks.** A "trusting…" or "skipping…" path that bypasses a
   required verification is a defect, not a pass. Fail loudly on verification
   errors. A proven reuse of successful checks is different from an unchecked skip.
4. **Fix at the source.** Change the structure producing waste, rather than
   wrapping it in a cache that hides only one symptom.
5. **Evidence before the catalog.** Mark hypotheses as suspected until measured.
   Every finding cites runs, jobs, semantic step names, log lines or files; every
   fix has a before/after receipt. Correct disproven diagnoses openly.
6. **Human approval at checkpoints.** Agree the baseline, gates, diagnosis,
   priority and exemptions; approve the proposed change before edits. Deliver
   one finding per PR for the user to review and merge, then confirm verification
   on the real PR pipeline and again after merge. Keep decisions in `.muda/`.
7. **No data is not zero.** Missing logs, inaccessible protection settings,
   truncated listings and unobserved image jobs mean unavailable evidence, not
   zero builds, zero warnings or no gates. Report coverage and uncertainty.

### Delivery principles D2–D6

- **D2 — Immutable deployed identity.** Record image identity in infrastructure
  code and deploy by digest, not a mutable tag. Isolate ad-hoc builds and use
  per-run overrides. An immutable-tag observation proves identity at that point
  in time only: policy can change and tags can move. Digests are the robust
  content identity; verify the resolved digest after promotion and deployment.
- **D3 — CI tests what ships.** Use one exact interpreter version source and the
  locked dependency set everywhere, including image builds, deploys and release
  jobs. A green gate must exercise the artifact that will actually run.
- **D4 — Retire dangerous unused tooling.** Verify usage from evidence before
  retiring a risky path; do not keep an unused unsafe mechanism "just in case".
- **D5 — Separate cache storage.** A layer cache needs a mutable store separate
  from immutable artifacts, isolated per consumer with expiry for superseded
  manifests. Configuring a cache is not evidence of a measured cache hit.
- **D6 — Every pin has a refresh path.** Assign an owner and a gated update path.
  Native auto-merge may be enabled only by user-approved policy on trustworthy
  gates; unattended green update PRs are not a working refresh mechanism.

## Catalog

| Class | Waste | Definition | Measurement | Illustrative example (not adopter proof) |
|---|---|---|---|---|
| W1 | **No-op not a no-op** | An unchanged deploy updates stacks or builds images. | Repeat an unchanged tree; account for each stack and asset, expecting zero updates and builds only when observations are complete. | Runtime metadata updates 8 unrelated stacks; rotating credentials change an asset hash. |
| W2 | **Unpinned toolchain** | Floating versions let the same tree produce different outputs. | Inspect action refs, runners, interpreter versions and base images; compare resolved versions. | A major-only interpreter pin resolves to different patches. |
| W3 | **Emulated builds** | Images build under CPU emulation rather than natively for the target. | Inspect every image job's runner architecture, target platform and emulation steps. | An install layer takes 7 minutes emulated but seconds natively. |
| W4 | **Uncached work** | Unchanged dependency installs or layers are recomputed. | Repeat identical inputs and inspect actual hit/miss evidence per asset and layer. | A rotating build argument invalidates all subsequent layers. |
| W5 | **Serial where it could be parallel** | Independent work is serial, or expensive computation is duplicated. | Profile tests and inspect the dependency graph before introducing bounded parallelism. | Three tests repeat the same minute-long computation. |
| W6 | **Re-verification** | Proven equivalent checks actually execute more than once on the same tested tree and branch. | Match executed jobs' commands, inputs, platform, environment and applicable workflow definitions, then count mirrors per proven tree. | CI and deploy execute the same suite; a deploy's skipped suite is not duplication. |
| W7 | **Chain stalls** | Automated hand-offs fail or stall without a visible outcome. | Follow chain links and classify actual error lines, cancellations and expected completion times separately from operator waits. | A pin-bump hand-off fails without raising an issue. |
| W8 | **Silent runner-plane failure** | Jobs queue because the runner plane cannot accept work, without alerting. | Compare queue age, runner currency, capacity rejection and launched-then-reaped-idle evidence. | An obsolete baked runner queues jobs for hours while provider status is green. |
| W9 | **Rebuilt artifact** | Identical inputs are built again across stages or pipeline steps. | Count build/push/skip outcomes by content identity across separate build and deploy jobs. | Two stages rebuild the same asset instead of promoting its digest. |
| W10 | **Ignored advance notice** | CI warnings about platform or toolchain changes go unread. | Group warning annotations by normalized signature and follow their sources and deadlines. | A runner retirement warning repeats for a week before jobs stop. |

W1 and W9 are agent diagnoses from logs and code, not stack-specific CLI parsing.
`slow-step` suggests investigation; it does not by itself prove either class.

## Candidate wastes

Suspicions stay unnumbered until a measured instance confirms them. Investigate
or rule out each with evidence, rather than promoting assumptions into rules.

- Concurrency groups cancel deploys mid-flight.
- Oversized container build contexts.
- Post-deploy gates run regardless of what changed (preserve required coverage).
- Network steps lack timeouts.
- Artifact storage grows without lifecycle policies.
- Automation PRs remain open for many days (distinguish operator approval waits).

## Exemptions

Prefer a fix. Keep waste only when removal would weaken a gate or the cost is
justified work. The user approves an exemption in `.muda/exemptions.toml`, with
all six fields:

| Field | Meaning |
|---|---|
| waste | W-number covered. |
| location | Stable semantic identity: file, workflow/job/check ID or named asset. |
| reason | Why this specific cost or gate change is justified, with evidence. |
| agreed-by | Person who approved the decision. |
| date | Date granted. |
| review-by | Expiry/review date when the decision must be raised again. |

Never identify a step by its ordinal index: inserting a step must not transfer
an exemption to a different check. Fail loudly on a stale, missing or ambiguous
semantic location; do not silently apply it elsewhere. Expired exemptions must
be raised for renewed approval, not treated as permanent permission. Gate
removals or loosening fail verification unless explicitly covered by an approved
exemption; they are not ordinary speed improvements.

An exemption whose reason is that the kept cost catches a bug class must name
the test that catches it. Reverting that bug's fix must turn the proposed faster
gate red; if it does not, the claim is unproven and the exemption is not granted
or renewed.

Track findings and receipts in `.muda/`, not a cross-project conformance table.
The normal path is `open` → `approved` → `in-pr` → `fixed`; failed verification
reopens a finding. Deliberately retained work is `exempt` and links its record.
Record a final measured baseline only after every finding is fixed or exempt.
