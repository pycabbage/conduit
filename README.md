# conduit

A Go relay that maintains Discord Gateway WebSocket connections on any always-on host and forwards events to Cloudflare Workers + Durable Objects (Hibernation API).

## Setup

```bash
go build .
CONFIG_FILE=/etc/conduit/config.json ./conduit
```

See [`example/config.sample.jsonc`](example/config.sample.jsonc) for the configuration format.

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
