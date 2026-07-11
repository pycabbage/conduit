/// <reference types="./node-api-headers.d.ts" />

import { type ChildProcess, spawn } from "child_process"
import { chmod, rm } from "fs/promises"
import { include_dir } from "node-api-headers"

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

async function buildBinding() {
  const bindingFilename = `conduit.${process.platform}-${process.arch}.node`
  await joinProcess(
    spawn(
      "go",
      [
        "build",
        "-tags=napi",
        "-buildmode=c-shared",
        "-o",
        `${import.meta.dirname}/../dist/${bindingFilename}`,
        "./cmd/conduit",
      ],
      {
        stdio: "inherit",
        cwd: `${import.meta.dirname}/../../../`,
        env: {
          ...process.env,
          CGO_ENABLED: "1",
          CGO_CFLAGS: `-I${include_dir}`,
        },
      }
    )
  )
  await rm(
    `${import.meta.dirname}/../dist/conduit.${process.platform}-${process.arch}.h`,
    { force: true }
  )
  await chmod(`${import.meta.dirname}/../dist/${bindingFilename}`, 0o755)
}

async function buildTs() {
  joinProcess(
    spawn("tsc", [], {
      stdio: "inherit",
      cwd: `${import.meta.dirname}/../`,
      env: process.env,
    })
  )
}

async function main() {
  await Promise.all([await buildBinding(), await buildTs()])
}

await main()
