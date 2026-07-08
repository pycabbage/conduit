#!/usr/bin/env node

import { readFileSync } from "node:fs"
import { reload, start, stop } from "./index.js"

const configFile = process.env.CONFIG_FILE || "/etc/conduit/config.json"

function readConfig(): string {
  return readFileSync(configFile, "utf8")
}

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

let shuttingDown = false

async function main(): Promise<void> {
  console.log(`conduit-relay: starting (CONFIG_FILE=${configFile})`)
  let configText: string
  try {
    configText = readConfig()
  } catch (err) {
    console.error(`conduit-relay: config read: ${errorMessage(err)}`)
    process.exitCode = 1
    return
  }

  try {
    await start(configText)
  } catch (err) {
    console.error(`conduit-relay: failed to start: ${errorMessage(err)}`)
    process.exitCode = 1
    return
  }
  console.log("conduit-relay: started")
}

process.on("SIGHUP", async () => {
  console.log("conduit-relay: SIGHUP received, reloading config")
  let configText: string
  try {
    configText = readConfig()
  } catch (err) {
    console.error(`conduit-relay: config read: ${errorMessage(err)}`)
    return
  }
  try {
    await reload(configText)
    console.log("conduit-relay: reload complete")
  } catch (err) {
    console.error(`conduit-relay: reload failed: ${errorMessage(err)}`)
  }
})

async function shutdown(signal: string): Promise<void> {
  if (shuttingDown) return
  shuttingDown = true
  console.log(`conduit-relay: ${signal} received, stopping`)
  try {
    await stop()
  } catch (err) {
    console.error(`conduit-relay: stop failed: ${errorMessage(err)}`)
  } finally {
    process.exit(0)
  }
}

process.on("SIGTERM", () => {
  void shutdown("SIGTERM")
})
process.on("SIGINT", () => {
  void shutdown("SIGINT")
})

void main()
