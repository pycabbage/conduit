/**
 * generate npm packages
 */

type Target = `${NodeJS.Platform}-${NodeJS.Architecture}`
const targets: Target[] = [
  "linux-x64",
  "linux-arm64",
  "darwin-x64",
  "darwin-arm64",
  "win32-x64",
  "win32-arm64"
]
