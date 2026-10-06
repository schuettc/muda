# Changelog

## 0.1.0

- First public release. muda finds where delivery pipelines waste time and fixes it with evidence: measure, diagnose, fix, verify.
- Commands: `measure`, `scan`, `gates`, `logs`, `notices`, `record`, `compare`, `recipes`, `recipe`, `standard` and `skills`, plus the family `version`, `update`, `commands`, `help` and `man`.
- The delivery-waste standard (W1 to W10) and recipes R1 to R11, with sections for GitHub Actions, CDK, Docker, Python, Node and Go.
- Five agent skills: `muda`, `muda-measure`, `muda-diagnose`, `muda-fix` and `muda-verify`. They ship in the binary (`muda skills install`), as a Claude Code plugin and as a pi package.
