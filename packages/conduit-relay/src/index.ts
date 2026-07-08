// Library entrypoint for `conduit-relay`. See
// docs/adr/0007-wasm-lifecycle-without-signals.md for why no signal handlers
// are registered here.

import { loadRelay } from "./load.js"

export type RelayConfig = string | object

let relayPromise: ReturnType<typeof loadRelay> | undefined

function getRelay(): ReturnType<typeof loadRelay> {
  if (!relayPromise) {
    relayPromise = loadRelay()
  }
  return relayPromise
}

function toConfigString(config: RelayConfig): string {
  return typeof config === "string" ? config : JSON.stringify(config)
}

export async function start(config: RelayConfig): Promise<void> {
  const relay = await getRelay()
  return relay.start(toConfigString(config))
}

export async function stop(): Promise<void> {
  const relay = await getRelay()
  return relay.stop()
}

export async function reload(config: RelayConfig): Promise<void> {
  const relay = await getRelay()
  return relay.reload(toConfigString(config))
}
