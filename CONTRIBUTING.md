# Contributing to muda

muda is a Go module, `github.com/schuettc/muda`, built on
[tools-common](https://github.com/schuettc/tools-common). It follows the
`.tools` family layout: one binary, one gate, one release workflow.

## Setup

You need Go (the version in `go.mod`), [just](https://just.systems),
[lefthook](https://lefthook.dev) and, for `just verify-extra`,
[actionlint](https://github.com/rhysd/actionlint).

Once per clone, install the git hooks:

```sh
just hooks
```

The pre-commit hook checks `gofmt`. The pre-push hook runs `just verify`.

## The gate

```sh
just verify
```

This is exactly what CI runs. It is the family Go gate (gofmt, vet,
golangci-lint with the family config, `go test -race`, and builds for darwin and
linux on arm64 and amd64), then `verify-extra`, which runs actionlint on the
workflows. There is no repo lint config. A real exception is a
`//nolint:<linter> // <why>` comment on the flagged line.

`just build` makes a local binary stamped the way a release is.

## Product rules

- **The CLI writes only `.muda/`, and only through `muda record`.** It never
  edits code, workflows or repository settings. Fixes are made by an agent, with
  a person's approval, in a pull request.
- **No data is not zero.** A missing API result, permission, gate or log is
  `unavailable` with a reason, never an empty success.
- **Every number names its evidence.** A run, job, step, log line or file.
- **Signal IDs are a contract.** Declare each one once, in `internal/signal`.
  Recipes are checked against the registry.

## No project facts

Shipped content names no real project: not in code, skills, recipes, the
standard or docs. Examples describe general patterns, not someone's pipeline.

Recorded test fixtures are the one exception. They may name their source, and
the fixtures under `testdata/tackle` are recorded from the public
[schuettc/tackle](https://github.com/schuettc/tackle) repository. Record new
fixtures with `go run ./internal/ghtest/cmd/record`, and review every file it
writes for secrets and private data before you commit it.

## The content checker

`internal/content` checks everything embedded in the binary: the standard,
the recipes and the skills (`content.go`). `go test ./cmd/muda` runs it over
the real content. It fails on:

- a recipe with a bad ID, or an unknown waste, signal or stack;
- a recipe missing a required section;
- a `sh` example whose `muda` command or flags the real CLI would reject;
- a project name from its project-facts list.

If you add a stack, add it to `recipes/stacks.txt`. If you add a waste class,
add it to the standard's catalog.

## Skills and their word guards

The five skills live in `skills/`. The same files are embedded in the binary,
and a test fails if the two copies differ.

- `internal/skills` lints every skill: frontmatter, ownership, checkpoints,
  and every `muda` command line in any code block, checked against the real
  command table.
- The contract tests in `cmd/muda/skills_test.go` are word guards. Each skill
  must keep certain load-bearing phrases (a STOP, a checkpoint, a status name)
  and must not use banned ones (for example "green is green", or "provisional").

When you change a skill's wording on purpose, change its guard in the same
commit, and say why in the message. A guard that fails by surprise has caught
a real regression.

## Versions and releases

`VERSION` is the release knob. Merging to `main` with a new `VERSION` releases
it: `release.yml` builds, signs, notarizes and publishes to GitHub and to
`muda.tools/dl`. A pull request into `main` must raise `VERSION`, or carry the
`no-release` label, or `version-guard` fails it. Keep
`.claude-plugin/plugin.json` at the same version (a test checks this), and add
an entry to `CHANGELOG.md`.

## Pull requests

One change per pull request. Write the test first, watch it fail, then make it
pass. Run `just verify` before you push.
