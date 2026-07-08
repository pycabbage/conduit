// Ambient types for the `Go` global that the vendored wasm_exec.js defines.

export {}

declare global {
  interface GoInstance {
    /** Import object to pass as the second argument to `WebAssembly.instantiate`. */
    importObject: WebAssembly.Imports
    /** Runs the given wasm instance. Resolves only when the Go program exits. */
    run(instance: WebAssembly.Instance): Promise<void>
  }

  var Go: {
    new (): GoInstance
  }
}
