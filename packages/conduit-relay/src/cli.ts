#!/usr/bin/env node

import { spawn } from "node:child_process"
import { getBindingPath } from "./loader"

const args = process.argv.slice(2)
const child = spawn(await getBindingPath(), args, {
  stdio: "inherit",
  env: process.env,
})

child.on("exit", (code) => process.exit(code ?? 1))

for (const sig of ["SIGHUP", "SIGTERM", "SIGINT"] as const) {
  process.on(sig, () => child.kill(sig))
}
