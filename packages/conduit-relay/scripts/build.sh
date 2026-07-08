#!/bin/bash

set -euo pipefail

REPO_ROOT=$(git rev-parse --show-toplevel)

function get_package_root() {
  echo "$REPO_ROOT/packages/conduit-relay"
  return 0
}

(
  cd "$(get_package_root)"
  bun tsc
)

(
  cd "${REPO_ROOT}"
  GOOS=js GOARCH=wasm go build -o packages/conduit-relay/dist/conduit.wasm ./cmd/conduit
)

(
  cd "$(get_package_root)"
  cat $(go env GOROOT)/lib/wasm/wasm_exec.js > "$(get_package_root)/dist/wasm_exec.js"
)
