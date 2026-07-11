/// <reference types="./node-api-headers.d.ts" />

import { type ChildProcess, spawn } from "node:child_process"
import { chmod, rm } from "node:fs/promises"
import { include_dir } from "node-api-headers"

const scriptsDir = import.meta.dirname
const packageRoot = `${scriptsDir}/../`
const repoRoot = `${scriptsDir}/../../../`
const distDir = `${packageRoot}dist`
const nativeDir = `${packageRoot}native`

async function joinProcess(proc: ChildProcess) {
  return new Promise<number | null>((resolve, reject) => {
    proc.on("error", (err) => {
      reject(err)
    })
    proc.on("exit", (code) => {
      resolve(code)
    })
  })
}

async function run(
  command: string,
  args: string[],
  cwd: string,
  env: NodeJS.ProcessEnv = process.env
) {
  const code = await joinProcess(
    spawn(command, args, { stdio: "inherit", cwd, env })
  )
  if (code !== 0) {
    throw new Error(`${command} ${args.join(" ")} exited with code ${code}`)
  }
}

async function buildTs() {
  await run("tsc", [], packageRoot)
}

async function buildCore() {
  await run(
    "go",
    [
      "build",
      "-tags=core",
      "-buildmode=c-shared",
      "-o",
      `${distDir}/libconduitcore.so`,
      "./cmd/conduit",
    ],
    repoRoot,
    { ...process.env, CGO_ENABLED: "1" }
  )
}

async function buildNapiShim() {
  const bindingFilename = `conduit.${process.platform}-${process.arch}.node`
  await run(
    "gcc",
    [
      "-shared",
      "-fPIC",
      `-I${include_dir}`,
      `-I${distDir}`,
      `${nativeDir}/napi_shim.c`,
      `-L${distDir}`,
      "-lconduitcore",
      "-Wl,-rpath,$ORIGIN",
      "-o",
      `${distDir}/${bindingFilename}`,
    ],
    packageRoot
  )
  await chmod(`${distDir}/${bindingFilename}`, 0o755)
}

async function buildCli() {
  const cliFilename = `conduit.${process.platform}-${process.arch}`
  await run(
    "gcc",
    [
      `-I${distDir}`,
      `${nativeDir}/cli.c`,
      `-L${distDir}`,
      "-lconduitcore",
      "-Wl,-rpath,$ORIGIN",
      "-o",
      `${distDir}/${cliFilename}`,
    ],
    packageRoot
  )
  await chmod(`${distDir}/${cliFilename}`, 0o755)
}

async function main() {
  await rm(distDir, { recursive: true, force: true })
  await buildCore()
  await Promise.all([buildTs(), buildNapiShim(), buildCli()])
  await rm(`${distDir}/libconduitcore.h`, { force: true })
}

await main()
