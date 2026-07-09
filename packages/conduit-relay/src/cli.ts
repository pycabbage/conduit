#!/usr/bin/env node

// `npx conduit-relay` entrypoint. Reads the config file named by CONFIG_FILE
// (default /etc/conduit/config.json), starts the relay, and mirrors the native
// binary's signal handling: SIGHUP reloads, SIGTERM/SIGINT stop and exit.

import { readFileSync } from "node:fs"
import { start } from "./index.js"

const configFile = process.env.CONFIG_FILE || "/etc/conduit/config.json"
const readConfig = () => readFileSync(configFile, "utf8")

const relay = await start(readConfig())
console.log(`conduit-relay: started (CONFIG_FILE=${configFile})`)

process.on("SIGHUP", () => {
  relay
    .reload(readConfig())
    .catch((err) => console.error("conduit-relay: reload failed:", err))
})
const shutdown = () => relay.stop().finally(() => process.exit(0))
process.on("SIGTERM", shutdown)
process.on("SIGINT", shutdown)
