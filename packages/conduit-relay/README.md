# conduit-relay

[`conduit`](../../README.md)（Discord Gateway <-> Cloudflare Workers relay）を
WebAssembly化し、Node.js向けに配布するnpmパッケージ。CLI（`npx conduit-relay`）と
ライブラリ（`start`/`stop`/`reload`）の両方で使える。

## Build

要Go・Bun。`dist/`はビルド成果物でgit管理外のため、使用前に必ず実行する。

```bash
bash scripts/build.sh
```

## CLI

```bash
CONFIG_FILE=/etc/conduit/config.json npx conduit-relay
```

`kill -HUP <pid>`で設定再読込、`SIGTERM`/`SIGINT`でgraceful stop。

## Library

```js
import { start, stop, reload } from 'conduit-relay';

await start(configArrayOrJSONCString);
await reload(updatedConfig);
await stop();
```

設計判断の詳細は [`docs/adr/0006`](../../docs/adr/0006-relay-wasm-cmd-layout.md)・
[`0007`](../../docs/adr/0007-wasm-lifecycle-without-signals.md)・
[`0008`](../../docs/adr/0008-conduit-relay-npm-package.md) を参照。
