# conduit-relay

[`conduit`](../../README.md)（Discord Gateway <-> Cloudflare Workers relay）を
native Node.jsアドオン（`.node`）としてビルドし、Node.js向けに配布するnpmパッケージ。
CLI（`npx conduit-relay`）とライブラリ（`start`/`stop`/`reload`）の両方で使える。

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

`conduit` はネイティブアドオン（`.node`）の生exportをそのまま公開する。
`start`/`reload` は設定JSON(C)文字列を受け取り、成功時は空文字列、失敗時は
エラーメッセージ文字列を返す（例外はthrowしない）。エラーで例外にしたい場合は
呼び出し側で戻り値をチェックする。

```js
import { conduit } from 'conduit-relay';

const err = conduit.start(JSON.stringify(configArray)); // or a JSONC string
if (err) throw new Error(err);

conduit.reload(JSON.stringify(updatedConfigArray));
conduit.stop();
```

設計判断の詳細は [`docs/adr/0006`](../../docs/adr/0006-relay-wasm-cmd-layout.md)・
[`0007`](../../docs/adr/0007-wasm-lifecycle-without-signals.md)・
[`0008`](../../docs/adr/0008-conduit-relay-npm-package.md)（いずれもSuperseded）・
[`0009`](../../docs/adr/0009-native-napi-addon-instead-of-wasm.md) を参照。
