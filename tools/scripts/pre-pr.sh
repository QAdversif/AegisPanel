#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# pre-pr.sh — run the CI-equivalent checks locally before pushing a
# PR. The goal is to catch the lint / test / markdown formatting
# failures that otherwise cost a 5+ minute round-trip through
# GitHub Actions. Every step matches a `*.yml` job under
# `.github/workflows/`; the script intentionally duplicates the
# commands (rather than calling `act` or `gh workflow run`) so
# the feedback is fast and offline-capable.
#
# Usage:
#   tools/scripts/pre-pr.sh                # full check
#   tools/scripts/pre-pr.sh --quick        # lint-only (no tests, no build)
#   tools/scripts/pre-pr.sh --backend      # backend only
#   tools/scripts/pre-pr.sh --frontend     # frontend only
#   tools/scripts/pre-pr.sh --docs         # markdownlint only
#
# Environment:
#   Mavis_MEMORY_MD — override the path to the agent's MEMORY.md
#   for the in-script size check. Default: $HOME/.minimax/agents/mavis/memory/MEMORY.md
#   (skipped silently if the file does not exist — e.g. on a CI
#   runner that does not have the agent data directory).
#
# Exit code 0 = ready to push. Non-zero = at least one step failed
# (the failing step's output is dumped verbatim to stderr so the
# operator can fix and re-run).
#
# The script is also installed as a git pre-push hook by
# `tools/scripts/install-pre-push.sh`; pushing a branch with a
# red pre-pr will refuse to send the refs.
#
# Requires: bash 4+, go 1.24+, node 20+, npm. The frontend
# `node_modules` is created on demand (`npm ci`); the script
# does NOT install Go toolchain components that ship with
# `go install` — golangci-lint v2 must already be on PATH
# (CI installs it from the `golangci-lint` action; the install
# step is documented in CONTRIBUTING.md).
#
# Two non-CI-mirror checks are added on top of the CI
# matrix (introduced 2026-08-06 after the v0.8.8 fail in
# which `integrationStubResolver` was overlooked by `go
# build ./...` + `go test -short ./...`):
#
# 1. `go test -count=1 -tags=integration -run='^$' ./...`
#    forces compilation of all test files (including
#    `//go:build integration`-tagged ones) without
#    running them. Catches the v0.8.8 class of failure
#    locally, before the PR is pushed.
# 2. Agent MEMORY.md size check (70KB soft warn / 80KB
#    hard fail) — the agent's own memory file is a
#    persistent cost paid on every turn via the
#    `<agent_memory_tail>` system reminder; 80KB is the
#    hard cap before the file has to be split into
#    `topics/<X>.md` and archived.

set -uo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT" || exit 1

# ---- args -----------------------------------------------------------------

QUICK=0
SCOPE="all"
while [[ $# -gt 0 ]]; do
    case "$1" in
        --quick)   QUICK=1; shift ;;
        --backend) SCOPE="backend"; shift ;;
        --frontend) SCOPE="frontend"; shift ;;
        --docs)    SCOPE="docs"; shift ;;
        -h|--help)
            sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'
            exit 0
            ;;
        *) echo "unknown arg: $1" >&2; exit 2 ;;
    esac
done

# ---- pretty output --------------------------------------------------------

# `tput` may be missing on stripped CI runners; fall back to
# plain text on the same terminal.
if command -v tput >/dev/null 2>&1 && tput setaf 1 >/dev/null 2>&1; then
    RED="$(tput setaf 1)"; GREEN="$(tput setaf 2)"
    YELLOW="$(tput setaf 3)"; BOLD="$(tput bold)"; RESET="$(tput sgr0)"
else
    RED=""; GREEN=""; YELLOW=""; BOLD=""; RESET=""
fi

# Track per-step timing and overall pass/fail.
declare -a STEP_NAMES=()
declare -a STEP_RESULTS=()
START_TS=$(date +%s)

# run_step NAME COMMAND_LABEL COMMAND...
#   Runs COMMAND...; on success prints green "[OK] NAME" with
#   elapsed seconds, on failure prints red "[FAIL] NAME" with
#   elapsed seconds, dumps the captured output, and returns the
#   command's exit code. The final summary prints only the
#   pass/fail aggregate.
run_step() {
    local name="$1"; shift
    local label="$1"; shift
    local step_start
    step_start=$(date +%s)
    echo "${BOLD}==> ${label}${RESET}"
    # Stream the output to a temp file so we can dump it on
    # failure without re-running. The `tee` keeps the live
    # feedback for the operator.
    local tmp
    tmp="$(mktemp)"
    if "$@" 2>&1 | tee "$tmp"; then
        local elapsed=$(( $(date +%s) - step_start ))
        echo "${GREEN}[OK]${RESET}   ${name}  (${elapsed}s)"
        STEP_NAMES+=("$name")
        STEP_RESULTS+=("ok")
        rm -f "$tmp"
        return 0
    else
        local rc=$?
        local elapsed=$(( $(date +%s) - step_start ))
        echo "${RED}[FAIL]${RESET} ${name}  (${elapsed}s, exit ${rc})"
        echo "${YELLOW}--- output ---${RESET}"
        cat "$tmp"
        echo "${YELLOW}--- end ---${RESET}"
        STEP_NAMES+=("$name")
        STEP_RESULTS+=("fail")
        rm -f "$tmp"
        return $rc
    fi
}

# ---- checks ---------------------------------------------------------------

do_gofmt() {
    run_step "backend gofmt" "gofmt -l backend/" \
        bash -c 'cd backend && gofmt -l . | (! grep .)'
}

do_go_build() {
    [[ $QUICK -eq 1 ]] && { echo "skip backend go build (--quick)"; return 0; }
    run_step "backend go build" "go build ./..." \
        bash -c 'cd backend && go build -trimpath ./...'
}

do_go_test() {
    [[ $QUICK -eq 1 ]] && { echo "skip backend go test (--quick)"; return 0; }
    run_step "backend go test (short)" "go test -short -count=1 ./..." \
        bash -c 'cd backend && go test -short -count=1 -timeout 120s ./...'
}

do_golangci() {
    run_step "backend golangci-lint" "golangci-lint run (GOFLAGS=-tags=integration)" \
        bash -c 'cd backend && GOFLAGS=-tags=integration golangci-lint run --config .golangci.yml ./...'
}

do_go_vet_integration() {
    # Compile test files behind `//go:build integration` WITHOUT
    # running them. The v0.8.8 PR (#189) shipped a `RefreshBearer`
    # method on `singbox.NodeResolver` and a smoke-test stub that
    # implemented the new method — but the integration-test stub
    # (`integrationStubResolver` in flushfn_integration_test.go)
    # was overlooked. `go build ./...` does NOT compile _test.go
    # files, and `go test -short ./...` skips build-tagged tests
    # entirely, so neither caught the missing-method error. CI
    # backend (ubuntu-latest/1.26) caught it at compile time.
    #
    # This step prevents the same class of failure: any change
    # to a public interface or its test-side stubs is forced
    # through `-tags=integration` compilation locally, before
    # the PR is pushed. `-run='^$'` ensures no tests actually
    # run (we only want the compile). `go test` is used (not
    # `go build`) because `go build` skips test files even
    # with the tag set.
    #
    # Reference: PR #189 (v0.8.8), the integrationStubResolver
    # fix commit `9647c9e`. The lesson is durable: any
    # build-tagged test file can rot silently under a
    # `go build ./...` + `go test -short ./...` workflow.
    [[ $QUICK -eq 1 ]] && { echo "skip backend go vet integration (--quick)"; return 0; }
    run_step "backend go vet (integration tags)" "go test -count=1 -tags=integration -run='^$' ./..." \
        bash -c 'cd backend && go test -count=1 -tags=integration -run="^$" -timeout 30s ./...'
}

do_npm_ci() {
    [[ $QUICK -eq 1 ]] && { echo "skip frontend npm ci (--quick)"; return 0; }
    # Skip the install if `node_modules` already exists; the
    # CI uses `npm ci` (clean) but a local dev typically has
    # `node_modules` from `npm install` and the install step
    # would re-download the world for nothing.
    if [[ -d frontend/node_modules ]]; then
        echo "skip frontend npm ci (node_modules already present)"
        return 0
    fi
    run_step "frontend npm ci" "npm ci --no-audit --no-fund" \
        bash -c 'cd frontend && npm ci --no-audit --no-fund'
}

do_codegen_check() {
    run_step "frontend codegen:check" "openapi-typescript up to date" \
        bash -c 'cd frontend && npm run codegen:check'
}

do_typecheck() {
    run_step "frontend type-check" "vue-tsc --noEmit" \
        bash -c 'cd frontend && npm run type-check'
}

do_eslint() {
    run_step "frontend lint" "eslint + check-raw-text" \
        bash -c 'cd frontend && npm run lint'
}

do_build() {
    [[ $QUICK -eq 1 ]] && { echo "skip frontend build (--quick)"; return 0; }
    run_step "frontend build" "vue-tsc + vite build" \
        bash -c 'cd frontend && npm run build'
}

do_memory_size_check() {
    # Memory hygiene: Mavis agent memory file is allowed
    # up to 80KB. Above 70KB we warn (and the quarterly
    # `memory-topics-prune` cron will trip a hard reset
    # above 80KB). The path is operator-specific — on
    # this machine it resolves to
    # `C:\Users\adversif\.minimax\agents\mavis\memory\MEMORY.md`
    # under Git Bash (`~` is the user home). On other
    # machines / CI runners, the path may not exist; we
    # skip silently in that case. Override with the
    # `Mavis_MEMORY_MD` env var to point at a custom path.
    #
    # Reference: 2026-08-06 memory hygiene session.
    # MEMORY.md was 313KB at start of session; after
    # topic extraction + archive by month, dropped to
    # 14.8KB. The 70KB soft warn / 80KB hard cap is the
    # durable threshold.
    local mem_path="${Mavis_MEMORY_MD:-${HOME}/.minimax/agents/mavis/memory/MEMORY.md}"
    if [[ ! -f "$mem_path" ]]; then
        echo "skip memory size check (no MEMORY.md at $mem_path)"
        return 0
    fi
    local size
    # `stat -c%s` is GNU (Linux / Git Bash); `stat -f%z`
    # is BSD/macOS. Try GNU first, fall back.
    size=$(stat -c%s "$mem_path" 2>/dev/null || stat -f%z "$mem_path" 2>/dev/null)
    if [[ -z "$size" || "$size" -eq 0 ]]; then
        echo "skip memory size check (could not stat $mem_path)"
        return 0
    fi
    local kb=$(( size / 1024 ))
    if [[ $size -gt 81920 ]]; then  # 80KB hard cap
        echo "${RED}[FAIL]${RESET} agent memory size: ${kb}KB (>80KB hard cap, MUST trim before push)"
        STEP_NAMES+=("agent memory size")
        STEP_RESULTS+=("fail")
        return 1
    elif [[ $size -gt 71680 ]]; then  # 70KB soft warn
        echo "${YELLOW}[WARN]${RESET} agent memory size: ${kb}KB (>70KB soft cap, consider trimming)"
        return 0
    else
        echo "[OK]   agent memory size: ${kb}KB (under 70KB cap)"
        return 0
    fi
}

do_markdownlint() {
    # markdownlint-cli2 is not a project dev dep; we use
    # `npx -y` to fetch it on first run. The CI pins a
    # specific version via the DavidAnson action; locally
    # we accept the latest 0.x. The glob set matches the
    # CI workflow with one addition: the local
    # `backups/` directory at the repo root holds
    # untracked pre-rewrite bundles + a stale readme
    # (see .gitignore `/backups/`); markdownlint
    # scans the filesystem, not the git index, so
    # those would show up as red here even though
    # CI never sees them. Same for `coverage/`
    # artefacts from a local `go test -coverprofile`.
    run_step "docs markdownlint" "markdownlint-cli2 on **/*.md" \
        bash -c 'npx -y markdownlint-cli2@0.17 "**/*.md" "!node_modules/**" "!**/node_modules/**" "!**/dist/**" "!**/.vuepress/.temp/**" "!**/.vuepress/.cache/**" "!backups/**" "!**/coverage/**"'
}

# ---- run ------------------------------------------------------------------

case "$SCOPE" in
    backend)
        do_gofmt;         FAIL+=$?
        do_go_build;      FAIL+=$?
        do_go_vet_integration; FAIL+=$?
        do_go_test;       FAIL+=$?
        do_golangci;      FAIL+=$?
        do_memory_size_check; FAIL+=$?
        ;;
    frontend)
        do_npm_ci;      FAIL+=$?
        do_codegen_check; FAIL+=$?
        do_typecheck;   FAIL+=$?
        do_eslint;      FAIL+=$?
        do_build;       FAIL+=$?
        do_memory_size_check; FAIL+=$?
        ;;
    docs)
        do_markdownlint; FAIL+=$?
        do_memory_size_check; FAIL+=$?
        ;;
    all)
        do_gofmt;         FAIL+=$?
        do_go_build;      FAIL+=$?
        do_go_vet_integration; FAIL+=$?
        do_go_test;       FAIL+=$?
        do_golangci;      FAIL+=$?
        do_npm_ci;        FAIL+=$?
        do_codegen_check; FAIL+=$?
        do_typecheck;     FAIL+=$?
        do_eslint;        FAIL+=$?
        do_build;         FAIL+=$?
        do_markdownlint;  FAIL+=$?
        do_memory_size_check; FAIL+=$?
        ;;
esac

# ---- summary --------------------------------------------------------------

TOTAL=$(( $(date +%s) - START_TS ))
echo
echo "${BOLD}==> Summary (${TOTAL}s)${RESET}"
fail_count=0
for i in "${!STEP_NAMES[@]}"; do
    name="${STEP_NAMES[$i]}"
    res="${STEP_RESULTS[$i]}"
    if [[ "$res" == "ok" ]]; then
        printf "  %s[OK]%s   %s\n" "$GREEN" "$RESET" "$name"
    else
        printf "  %s[FAIL]%s %s\n" "$RED" "$RESET" "$name"
        fail_count=$(( fail_count + 1 ))
    fi
done

if [[ $fail_count -eq 0 ]]; then
    echo
    echo "${GREEN}${BOLD}Ready to push.${RESET}"
    exit 0
else
    echo
    echo "${RED}${BOLD}${fail_count} step(s) failed; fix and re-run.${RESET}"
    exit 1
fi
