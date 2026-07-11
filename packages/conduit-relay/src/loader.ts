import { stat } from "node:fs/promises"
import { createRequire } from "node:module"
import { join } from "node:path"

export interface ConduitAddon {
  start: (configJSON: string) => string
  stop: () => void
  reload: (configJSON: string) => string
}

interface Require extends NodeJS.Require {
  <T = unknown>(id: string): T
}

const nativeRequire = createRequire(import.meta.url) as Require

function validateVersion(bindingPackage: string) {
  const { version: currentVersion } = nativeRequire<{ version: string }>(
    `../package.json`
  )
  const { version: bindingVersion } = nativeRequire<{ version: string }>(
    `${bindingPackage}/package.json`
  )
  if (currentVersion !== bindingVersion) {
    throw new Error(
      `Version mismatch: current version is ${currentVersion}, but binding version is ${bindingVersion}`
    )
  }
}

export function getBinding() {
  const bindingFilepath = `./conduit.${process.platform}-${process.arch}.node`
  const bindingPackage = `@conduit-relay/conduit-${process.platform}-${process.arch}`
  const loadErrors: unknown[] = []
  try {
    return nativeRequire<ConduitAddon>(bindingFilepath)
  } catch (err) {
    loadErrors.push(err)
    try {
      const binding = nativeRequire<ConduitAddon>(bindingPackage)
      validateVersion(bindingPackage)
      return binding
    } catch (err) {
      loadErrors.push(err)
      const error = new Error(
        `Cannot find binding ${process.platform}-${process.arch}.node`
      )
      error.cause = (loadErrors as Error[]).reduce((err, cur) => {
        cur.cause = err
        return cur
      })
      throw error
    }
  }
}

async function exists(path: string) {
  try {
    await stat(path)
    return true
  } catch {
    return false
  }
}

export async function getBindingPath() {
  const bindingPath = join(
    import.meta.dirname,
    `./conduit.${process.platform}-${process.arch}`
  )
  if (await exists(bindingPath)) {
    return bindingPath
  }
  const bindingPackage = `@conduit-relay/conduit-${process.platform}-${process.arch}`
  try {
    const resolvedBindingPath = nativeRequire.resolve(
      `${bindingPackage}/dist/conduit.${process.platform}-${process.arch}`
    )
    validateVersion(bindingPackage)
    return resolvedBindingPath
  } catch {
    throw new Error(`Cannot find binding ${process.platform}-${process.arch}`)
  }
}
