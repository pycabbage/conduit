#!/bin/bash

set -euo pipefail

sudo chown $(id -u):$(id -g) $HOME/.claude

# Install Claude Code
curl -fsSL https://claude.ai/install.sh | bash

# Install Bun
curl -fsSL https://bun.sh/install | bash

# Install pnpm
curl -fsSL https://get.pnpm.io/install.sh | sh -
