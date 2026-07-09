// Library entrypoint for `conduit-relay`.
//
// This loads the native Node-API addon (`conduit.node`) which wraps the same
// relay.Manager as the native CLI. See
// docs/adr/0009-native-napi-addon-instead-of-wasm.md for the rationale (native
// shared library instead of wasm; no Go->JS callback bridge; one relay per
// process).
//
// `conduit.node` is a build artifact produced by scripts/build.sh and does not
// exist in source. It is a CommonJS native addon, so we bridge from ESM via
// createRequire and resolve the sibling artifact with a runtime computed URL
// (per the ADR 0008 convention of resolving build artifacts at runtime rather
// than as static imports, which cannot be written before the artifact exists).

import { createRequire } from "node:module"

export type RelayConfig = string | object

export interface Relay {
  stop: () => Promise<void>
  reload: (config: RelayConfig) => Promise<void>
}

// Shape of the native addon's exports. start/reload return an empty string on
// success and a non-empty error message on failure (see main_napi.go); stop
// returns undefined.
interface ConduitAddon {
  start: (configJSON: string) => string
  stop: () => void
  reload: (configJSON: string) => string
}

const nodeRequire = createRequire(import.meta.url)

let addon: ConduitAddon | undefined

function getAddon(): ConduitAddon {
  if (!addon) {
    const addonPath = new URL("./conduit.node", import.meta.url).pathname
    addon = nodeRequire(addonPath) as ConduitAddon
  }
  return addon
}

function toConfigString(config: RelayConfig): string {
  return typeof config === "string" ? config : JSON.stringify(config)
}

function throwOnError(result: string): void {
  if (result !== "") {
    throw new Error(result)
  }
}

export async function start(config: RelayConfig): Promise<Relay> {
  const mod = getAddon()
  throwOnError(mod.start(toConfigString(config)))

  return {
    stop: async () => {
      mod.stop()
    },
    reload: async (next: RelayConfig) => {
      throwOnError(mod.reload(toConfigString(next)))
    },
  }
}
