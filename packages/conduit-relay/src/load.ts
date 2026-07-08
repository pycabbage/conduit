import "./wasm_exec.js"
import conduitWasm from "./conduit.wasm"

declare global {
  var __conduitReady: boolean | undefined
}

const READY_TIMEOUT_MS = 10_000

function callGo(name: string, ...args: unknown[]): Promise<void> {
  return new Promise((resolve, reject) => {
    const fn = (globalThis as unknown as Record<string, unknown>)[name]
    if (typeof fn !== "function") {
      reject(
        new Error(
          `conduit-relay: global "${name}" is not exposed by the wasm module`
        )
      )
      return
    }
    ;(fn as (...fnArgs: unknown[]) => void)(...args, resolve, reject)
  })
}

function waitForReady(timeoutMs: number): Promise<void> {
  const startedAt = Date.now()
  return new Promise((resolve, reject) => {
    ;(function poll() {
      if (__conduitReady === true) {
        resolve()
        return
      }
      if (Date.now() - startedAt > timeoutMs) {
        reject(
          new Error(
            "conduit-relay: timed out waiting for the wasm module to become ready"
          )
        )
        return
      }
      setTimeout(poll, 0)
    })()
  })
}

export interface Relay {
  start: (config: string) => Promise<void>
  stop: () => Promise<void>
  reload: (config: string) => Promise<void>
}

let loadPromise: Promise<Relay> | undefined

export function loadRelay(): Promise<Relay> {
  if (!loadPromise) {
    loadPromise = (async () => {
      const go = new Go()

      const { instance } = await WebAssembly.instantiate(
        conduitWasm,
        go.importObject
      )

      go.run(instance).catch((err: unknown) => {
        console.error("conduit-relay: wasm runtime exited unexpectedly:", err)
      })

      await waitForReady(READY_TIMEOUT_MS)

      return {
        start: (configString: string) => callGo("start", configString),
        stop: () => callGo("stop"),
        reload: (configString: string) => callGo("reload", configString),
      }
    })()
  }
  return loadPromise
}
