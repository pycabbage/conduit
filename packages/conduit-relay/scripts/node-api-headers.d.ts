declare module "node-api-headers" {
  export const include_dir: string
  interface Def {
    js_native_api_def: string
    node_api_def: string
  }
  export const def_paths: Def
  export const symbols: Record<
    "v1" | "v2" | "v3" | "v4" | "v5" | "v6" | "v7" | "v8" | "v9" | "v10",
    Def
  >
}
