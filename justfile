# ---- .tools family standard: identical in every family repo ----------------
# `just verify` is exactly what CI runs: this tool's `prepare` (files the build
# needs, e.g. an embedded asset), the family gate (tools-actions go-ci, at the
# version .github/workflows/ci.yml pins), then this tool's `verify-extra`.
# The pre-push hook (lefthook.yml) runs it too, so local and CI never differ.
set shell := ["bash", "-euo", "pipefail", "-c"]

default: verify

verify: prepare gate verify-extra

# Everything: verify plus this tool's slow checks (browser, containers), which
# CI runs as their own required jobs.
verify-all: verify verify-slow

# The family Go gate: gofmt, vet, golangci-lint (family config), race tests,
# cross-build. Fetched once per tools-actions version into ~/.cache.
gate:
    #!/usr/bin/env bash
    set -euo pipefail
    v="$(grep -oE 'go-ci@v[0-9]+\.[0-9]+\.[0-9]+' .github/workflows/ci.yml | head -1 | cut -d@ -f2)"
    f="${XDG_CACHE_HOME:-$HOME/.cache}/tools-actions/$v/go-ci/local.sh"
    [ -f "$f" ] || { mkdir -p "$(dirname "$f")"; curl -fsSL "https://raw.githubusercontent.com/schuettc/tools-actions/$v/go-ci/local.sh" -o "$f"; }
    bash "$f"

fmt:
    gofmt -w $(git ls-files '*.go')

# Install the lefthook hooks into this clone's own .git/hooks (once per
# clone). A global core.hooksPath (casebook's recorder) forwards to them;
# plain `lefthook install` refuses to run under one.
hooks:
    #!/usr/bin/env bash
    set -euo pipefail
    d="$(cd "$(git rev-parse --git-common-dir)" && pwd)/hooks"
    git config --local core.hooksPath "$d"
    trap 'git config --local --unset core.hooksPath' EXIT
    lefthook install --force >/dev/null
    echo "lefthook hooks installed in $d"

# ---- muda -------------------------------------------------------------------
# Files the gate needs that are not committed (built before the gate, locally
# and in CI). muda has none: the standard, recipes and skills it embeds are
# committed.
prepare:

# Slow checks (a browser, a container) that CI runs as their own jobs. muda has none.
verify-slow:

# Tool-specific checks beyond the gate (CI runs this too): actionlint on the
# workflows.
verify-extra:
    actionlint

# A local binary stamped the way a release is.
build:
    go build -ldflags "-X github.com/schuettc/muda/internal/version.version=$(cat VERSION) -X github.com/schuettc/muda/internal/version.commit=$(git rev-parse --short HEAD) -X github.com/schuettc/muda/internal/version.date=$(date -u +%Y-%m-%d)" -o muda ./cmd/muda
