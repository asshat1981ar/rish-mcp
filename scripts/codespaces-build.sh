#!/usr/bin/env bash
# Full local verification pipeline for GitHub Codespaces (or any Linux host with
# Docker). Mirrors CONTRIBUTING.md "Local checks" and .github/workflows/ci.yml:
#
#   1. Go server: gofmt check, vet, test -race, build, static CGO_ENABLED=0
#      binaries (exactly what server/Dockerfile compiles).
#   2. Docker images: server/Dockerfile relay + publicserver targets.
#   3. Android: containerized toolchain (app/Dockerfile.build), unit tests,
#      debug APK.
#   4. Node (cli + web): only when npm is available.
#
# Usage:
#   bash scripts/codespaces-build.sh
#
# Environment overrides:
#   SKIP_DOCKER=1   skip Docker image + Android steps (Go/Node only)
#   SKIP_ANDROID=1  skip only the Android toolchain build
#   SKIP_NODE=1     skip cli/web npm checks
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

SKIP_DOCKER="${SKIP_DOCKER:-0}"
SKIP_ANDROID="${SKIP_ANDROID:-0}"
SKIP_NODE="${SKIP_NODE:-0}"

pass=()
skip=()

step() { printf '\n\033[1;36m==> %s\033[0m\n' "$1"; }
ok()   { pass+=("$1"); printf '\033[1;32m[OK]\033[0m %s\n' "$1"; }
skip() { skip+=("$1: $2"); printf '\033[1;33m[SKIP]\033[0m %s (%s)\n' "$1" "$2"; }

# ---------------------------------------------------------------------------
step "1/4 Go server: gofmt, vet, test -race, build"
cd "$REPO_ROOT/server"

unformatted="$(gofmt -l .)"
if [[ -n "$unformatted" ]]; then
  printf 'Run gofmt on:\n%s\n' "$unformatted" >&2
  exit 1
fi

go vet ./...

# CONTRIBUTING.md: run -race "when the host has CGO and a supported C compiler".
# Some kernels (e.g. Android/proot with 39-bit VMA) cannot run ThreadSanitizer
# at all, so probe a trivial -race binary before committing to it.
race_ok=0
if command -v "$(go env CC)" >/dev/null 2>&1; then
  probe_dir="$(mktemp -d)"
  printf 'package main\n\nfunc main() {}\n' >"$probe_dir/main.go"
  if (cd "$probe_dir" && go mod init probe >/dev/null 2>&1 && CGO_ENABLED=1 go run -race . >/dev/null 2>&1); then
    race_ok=1
  fi
  rm -rf "$probe_dir"
fi
if [[ "$race_ok" == "1" ]]; then
  go test -race -count=1 ./...
  ok "go test -race"
else
  go test -count=1 ./...
  ok "go test (race unavailable on this host; CI runs -race)"
fi
go build ./...
ok "go build + vet + gofmt"

step "1b/4 Go server: static binaries (server/Dockerfile build stage)"
mkdir -p "$REPO_ROOT/dist"
CGO_ENABLED=0 go build -o "$REPO_ROOT/dist/relay" ./cmd/relay
CGO_ENABLED=0 go build -o "$REPO_ROOT/dist/publicserver" ./cmd/publicserver
CGO_ENABLED=0 go build -o "$REPO_ROOT/dist/setup" ./cmd/setup
ok "static relay/publicserver/setup binaries in dist/"
cd "$REPO_ROOT"

# ---------------------------------------------------------------------------
if [[ "$SKIP_DOCKER" == "1" ]]; then
  skip "Docker images" "SKIP_DOCKER=1"
  skip "Android" "SKIP_DOCKER=1"
elif ! command -v docker >/dev/null 2>&1; then
  skip "Docker images" "docker not available"
  skip "Android" "docker not available"
else
  step "2/4 Docker images: relay + publicserver targets"
  docker build --target relay -t rishmcp-relay server
  docker build --target publicserver -t rishmcp-public server
  ok "docker images rishmcp-relay, rishmcp-public"

  if [[ "$SKIP_ANDROID" == "1" ]]; then
    skip "Android" "SKIP_ANDROID=1"
  else
    step "3/4 Android: containerized unit tests + debug APK"
    docker build -t rishmcp-android-build -f app/Dockerfile.build app
    docker run --rm -v "$PWD/app:/work" -w /work rishmcp-android-build \
      gradle --no-daemon testDebugUnitTest assembleDebug
    apk="app/app/build/outputs/apk/debug/app-debug.apk"
    test -f "$apk"
    ls -lh "$apk"
    ok "Android tests + $apk"
  fi
fi

# ---------------------------------------------------------------------------
if [[ "$SKIP_NODE" == "1" ]]; then
  skip "Node cli/web" "SKIP_NODE=1"
elif ! command -v npm >/dev/null 2>&1; then
  skip "Node cli/web" "npm not available"
else
  step "4/4 Node: setup CLI tests + web lint/build"
  (cd cli && npm ci && npm test && npm pack --dry-run)
  (cd web && NEXT_TELEMETRY_DISABLED=1 npm ci && npm run lint && npm run build)
  ok "cli npm test + web lint/build"
fi

# ---------------------------------------------------------------------------
printf '\n\033[1mSummary\033[0m\n'
for item in "${pass[@]}"; do printf '  \033[1;32mPASS\033[0m %s\n' "$item"; done
for item in ${skip[@]+"${skip[@]}"}; do printf '  \033[1;33mSKIP\033[0m %s\n' "$item"; done
printf '\nAll requested checks completed.\n'
