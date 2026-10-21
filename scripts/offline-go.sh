#!/usr/bin/env bash
# Use only a prepared native toolchain and module cache; never fetch dependencies.
set -e
# Keep upstream helper semantics (optional unset globals and grep -q pipelines).
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"
export GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local CGO_ENABLED=1
# Upstream dependency helpers may suggest a download. Explicitly forbid it here.
curl() { echo 'offline build: curl is disabled' >&2; return 1; }
wget() { echo 'offline build: wget is disabled' >&2; return 1; }
uv() { echo 'offline build: dependency installation is disabled' >&2; return 1; }
export -f curl wget uv
source "$ROOT_DIR/build.sh" >&2
_ensure_cl100k_table >&2 || exit 1
pkg-config --exists libpcre2-8 || { echo 'prepared PCRE2 development package required' >&2; exit 1; }
build_cpp >&2
cd "$ROOT_DIR"
setup_cgo_env >&2
[[ "$CGO_LDFLAGS" == *libonnxruntime* ]] || { echo 'static ONNX Runtime required' >&2; exit 1; }
case "${1:-}" in
  test|vet|build|list) go "$@" ;;
  *) echo 'usage: scripts/offline-go.sh {test|vet|build|list} [go arguments]' >&2; exit 2 ;;
esac
