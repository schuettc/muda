# Changelog

## 0.1.10

- Recipes R13 (key the dependency layer on third-party dependencies only) and R14 (deploy contracts first, then every stack at once), from one adopter's measured deploys.

## 0.1.9

- `muda record finding add`, `finding set` and `exempt` check the `waste` they write against the standard's catalog or its candidate wastes (`candidate:<slug>`, with no recipe), and the `recipe` against the recipes (#21). Stored records are not re-judged, so removing a candidate or recipe later breaks no record. A location may now be `file:PATH`, for code outside workflows (#22).

## 0.1.0

- First public release. muda finds where delivery pipelines waste time and fixes it with evidence: measure, diagnose, fix, verify.
- Commands: `measure`, `scan`, `gates`, `logs`, `notices`, `record`, `compare`, `recipes`, `recipe`, `standard` and `skills`, plus the family `version`, `update`, `commands`, `help` and `man`.
- The delivery-waste standard (W1 to W10) and recipes R1 to R11, with sections for GitHub Actions, CDK, Docker, Python, Node and Go.
- Five agent skills: `muda`, `muda-measure`, `muda-diagnose`, `muda-fix` and `muda-verify`. They ship in the binary (`muda skills install`), as a Claude Code plugin and as a pi package.
