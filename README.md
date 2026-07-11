# conduit

A Go relay that maintains Discord Gateway WebSocket connections on any always-on host and forwards events to Cloudflare Workers + Durable Objects (Hibernation API).

## Setup

```bash
go build ./cmd/conduit
CONFIG_FILE=/etc/conduit/config.json ./conduit
```

See [`example/config.sample.jsonc`](example/config.sample.jsonc) for the configuration format.

## npm / npx (Node.js)

conduit's core relay logic is also built as a native Node.js addon (`.node`) and published for Node.js as [`packages/conduit-relay`](packages/conduit-relay), usable both as a CLI and as a library. This is the same relay loop as the native binary above -- just a different entrypoint (see `cmd/conduit/main.go` vs `cmd/conduit/main_core.go`).

```bash
cd packages/conduit-relay
bun run build   # builds dist/libconduitcore.so, dist/conduit.<platform>-<arch>.node, dist/conduit.<platform>-<arch> (requires a local Go toolchain, gcc, and Bun)

CONFIG_FILE=/etc/conduit/config.json npx conduit-relay
```

```js
import { conduit } from 'conduit-relay';

const err = conduit.start(JSON.stringify(configArray)); // or a JSONC string
if (err) throw new Error(err);

conduit.reload(JSON.stringify(updatedConfigArray));
conduit.stop();
```

Requires Node.js >= 22 (for the stable global `WebSocket` client conduit's outgoing Gateway/Worker connections rely on). See [`packages/conduit-relay/README.md`](packages/conduit-relay/README.md) for details.

<details>
<summary>Core Concepts</summary>

**The problem**

Hosting a Discord bot on Cloudflare alone has two fundamental constraints:

- Cloudflare blocks outgoing connections to `wss://gateway.discord.gg` (Discord's IP range returns 401).
- The Durable Objects Hibernation API only works for *incoming* WebSockets. A DO holding an outgoing connection can never hibernate, meaning Duration billing runs continuously.

**What conduit solves**

conduit runs on any always-on host (a VPS, home server, Raspberry Pi, etc.) and owns the outgoing Discord Gateway connection. It forwards every Gateway event to your Worker over a second WebSocket (outgoing from conduit, *incoming* to the DO), so the DO can use the Hibernation API and wake only when an event arrives.

```
Discord Gateway
    ↕ WS (outgoing — conduit side)
conduit on any always-on host
    ↕ WS (outgoing — conduit side / incoming — DO side)
Cloudflare Worker  →  Durable Object (Hibernation API)
```

</details>
