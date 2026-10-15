# Changelog

## 0.1.17

- muda-verify: an experiment that misses its pass bar but whose saving the human explicitly accepts (gates passing) is marked fixed, with the bar, the result, who accepted it and why in its estimate (#9).

## 0.1.16

- muda-diagnose splits a slow step that builds images into BuildKit phases (install, layer export, cache export, publish) before matching recipes, whatever stacks were confirmed (#12).

## 0.1.15

- Skills: a fix that changes an asset's identity, or shows only on one kind of change, records `Observe on:` in its estimate; muda-fix plans that deploy as its check and muda-verify judges only matching deploys (#8). R4 names the first-deploy rebuild.

## 0.1.14

- `muda compare --gates-base REF`: the gate check's before side is derived at REF instead of read from the recorded inventory, so a fix PR's gate proof shows only what it changes; the record's exemptions still apply, and `before_ref` names REF. It conflicts with `--gates-file`. muda-verify uses the PR's merge base in both phases, so a workflow changed only by another merged PR no longer reads UNVERIFIED (#7).

## 0.1.13

- Recipe R13: key the dependency layer on a lock export, not a resolve during the build, and install it with `uv pip install`; measured at one adopter.

## 0.1.12

- `muda record finding set`: an `exempt` finding whose waste a merged PR removed can become `fixed`, on the same proof as an `in-pr` one (a passing receipt for that PR); its exemption becomes history (#29).

## 0.1.11

- A sixth skill, `muda-design`: on request, it checks a planned pipeline or deploy change against the recipes for the stacks it touches, before code is written. It reads each recipe's Symptom, Fix, "After" shape and Traps (not Detect: there are no runs yet), recommends changes labelled expected, not measured, and never writes `.muda/` or weakens a gate. The entry skill routes design requests to it.

## 0.1.10

- Recipes R13 (key the dependency layer on third-party dependencies only) and R14 (deploy contracts first, then every stack at once), from one adopter's measured deploys.

## 0.1.9

- `muda record finding add`, `finding set` and `exempt` check the `waste` they write against the standard's catalog or its candidate wastes (`candidate:<slug>`, with no recipe), and the `recipe` against the recipes (#21). Stored records are not re-judged, so removing a candidate or recipe later breaks no record. A location may now be `file:PATH`, for code outside workflows (#22).

## 0.1.8

- `compare --gates`: a ruleset read from another timezone compares by instant, so a timezone change no longer reads as a security change (#23).

## 0.1.7

- Recipe R12 (split a service rollout into phases before tuning it) and a CDK section in R5 for parallel image asset builds.

## 0.1.6

- The standard lists lingering expand as a candidate waste.

## 0.1.5

- Recipe submissions: diagnose and verify offer to draft one, with a public-text rule that keeps project details out; an issue form on the repository.

## 0.1.4

- Skills size proof to the change's risk, timebox agent work, and read the pipeline's logs before building a proof.

## 0.1.3

- `record`: a cloned record without `receipts/` is valid (#5).

## 0.1.2

- `compare`: a record made before a repository rename compares as the same repository (#3).

## 0.1.1

- `record`: a URL password that is a whole shell or context reference is not a detected secret (#1).

## 0.1.0

- First public release. muda finds where delivery pipelines waste time and fixes it with evidence: measure, diagnose, fix, verify.
- Commands: `measure`, `scan`, `gates`, `logs`, `notices`, `record`, `compare`, `recipes`, `recipe`, `standard` and `skills`, plus the family `version`, `update`, `commands`, `help` and `man`.
- The delivery-waste standard (W1 to W10) and recipes R1 to R11, with sections for GitHub Actions, CDK, Docker, Python, Node and Go.
- Five agent skills: `muda`, `muda-measure`, `muda-diagnose`, `muda-fix` and `muda-verify`. They ship in the binary (`muda skills install`), as a Claude Code plugin and as a pi package.
