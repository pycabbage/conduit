#!/bin/bash

set -euo pipefail

REPO_ROOT=$(git rev-parse --show-toplevel)
PACKAGE_ROOT="$REPO_ROOT/packages/conduit-relay"

# 0. Start from a clean dist so stale artifacts from previous builds do not
#    linger (dist/ is a build-output directory, git-ignored).
rm -rf "$PACKAGE_ROOT/dist"

# 1. Compile the TypeScript sources (src -> dist).
(
  cd "$PACKAGE_ROOT"
  bun tsc
)

# 2. Build the native CLI binary (used by `npx conduit-relay` via cli.ts, which
#    just spawns it; see ADR 0009). This is the plain, tag-free native build of
#    cmd/conduit/main.go — it reads CONFIG_FILE and handles SIGHUP/SIGTERM/SIGINT
#    itself, so the TS launcher stays trivial. CGO is disabled (no cgo here,
#    unlike the addon below).
(
  cd "$REPO_ROOT"
  CGO_ENABLED=0 go build -o "$PACKAGE_ROOT/dist/conduit" ./cmd/conduit
)

# 3. Build the native Node-API addon (library runtime; see ADR 0009).
#    node_api.h is provided by the node-api-headers npm package. Resolve its
#    include directory at build time so no environment-specific path is baked
#    into the repo (ADR 0009). Resolve from PACKAGE_ROOT so the package's own
#    node_modules copy is preferred.
NAPI_INCLUDE=$(cd "$PACKAGE_ROOT" && bun -e 'console.log(require("node-api-headers").include_dir)')
(
  cd "$REPO_ROOT"
  CGO_ENABLED=1 CGO_CFLAGS="-I$NAPI_INCLUDE" \
    go build -tags=napi -buildmode=c-shared \
    -o "$PACKAGE_ROOT/dist/conduit.node" ./cmd/conduit
)

# go build -buildmode=c-shared always emits a C header alongside the shared
# library; it's for C/C++ consumers linking against the addon and is unused
# here (Node-API modules are loaded via require(), not a C header).
rm -f "$PACKAGE_ROOT/dist/conduit.h"
