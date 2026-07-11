# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## アーキテクチャ概要

conduit は Discord Bot を Cloudflare Workers + Durable Objects でホストするためのリレー基盤。

```
Discord Gateway
    ↕ WS (Outgoing)
conduit          ← Go製リレー。Cloudflare外VMで常時稼働
    ↕ WS (Outgoing)
Worker (fetch)  ← HTTP Upgradeを受けてDO stubに委譲して即終了
    → DO        ← Hibernation APIを使用。イベント受信時のみ起動
```

- Bot設定ファイルを読んで各BotのgoroutineをSIGHUP（またはNode-API経由）トリガーで差分管理する。中核ロジックは `internal/relay`（`Manager`, `BotConfig`, Discord Gateway ↔ Worker中継ループ）に切り出されており、プラットフォーム固有のエントリポイントは `cmd/conduit/` に同居し、それぞれビルドタグで分離されている（別ディレクトリに分けると、片方のビルドタグ向けビルド時にもう一方のディレクトリが「ビルド対象ファイル0個のパッケージ」になりgoreleaserのビルドが壊れるため、同一ディレクトリに置く設計）:
  - `cmd/conduit/main.go`（`//go:build !core`）: ネイティブCLI。`os/signal`でSIGHUP/SIGTERM/SIGINTを処理。VM/Docker常駐用（`CGO_ENABLED=0`）
  - `cmd/conduit/main_core.go`（`//go:build core`）: `packages/conduit-relay`のnpmパッケージ向け。Node-API依存を持たない生C ABI（`ConduitStart`/`ConduitStop`/`ConduitReload`、`*C.char`で入出力）を`//export`し、`-buildmode=c-shared`で`libconduitcore.so`としてビルドする。`internal/relay`をこのnpmパッケージ向けに機械語へコンパイルする場所はここだけ
- **`packages/conduit-relay/`**: 上記`libconduitcore.so`を使い、Node.js向けnpmパッケージとして配布。`npx conduit-relay`でのCLI起動と、`start`/`stop`/`reload`関数のライブラリ利用の両方に対応（詳細は `packages/conduit-relay/README.md`）。Node-APIグルー（`packages/conduit-relay/native/napi_shim.c`）とCLI実行体（`packages/conduit-relay/native/cli.c`）は両方pure C（Goを含まない）で書かれ、`libconduitcore.so`を`-Wl,-rpath,$ORIGIN`で動的リンクする（設計判断は `docs/adr/0009` を参照）
- **example/sample-worker/src/index.ts** 一枚。Worker（薄いWSルーター）とConduitDO（Hibernation APIサーバー）を同居させたサンプル
- WS接続確立直後にconduitがinitメッセージ（`{"type":"init","token":"Bot ..."}`）を送りDOが`ctx.storage`に保存する（事前設定不要）

設計判断の根拠は `docs/adr/` を参照。

## コメント規約

コード（Go・TypeScript問わず）にコメントを書かない。設計判断・背景・WHY・
実装上の注意点は全て `docs/adr/` にADRとして記録し、コード上には残さない。
既存コードにコメントを見つけた場合は、その内容を対応するADR（無ければ
新規作成）に移し、コードからは削除する。

## conduit（Go）

```bash
# ビルド（ネイティブ）
go build ./cmd/conduit

# ローカル実行（設定ファイルを指定）
CONFIG_FILE=./example/config.sample.jsonc go run ./cmd/conduit

# Dockerイメージビルド（リポジトリルートから実行）
docker build -t conduit .

# 設定再読み込み（ホットリロード）
kill -HUP <pid>
```

依存パッケージは `github.com/coder/websocket` のみ（`internal/relay`・`cmd/conduit/main.go`・`cmd/conduit/main_core.go`共通）。

## packages/conduit-relay（Node.js / npm）

TypeScript・ESM・`src/`レイアウト。ネイティブ成果物はGo（`cmd/conduit/main_core.go`）1つ+ pure C（`native/napi_shim.c`, `native/cli.c`）2つの3成果物構成。ビルドは`scripts/build.ts`（Bunで実行するTSスクリプト）。

```bash
cd packages/conduit-relay

bun run build   # dist/libconduitcore.so, dist/conduit.<platform>-<arch>.node, dist/conduit.<platform>-<arch>（拡張子なし）, dist/*.js, dist/*.d.ts を生成（要ローカルGoツールチェイン・gcc・Bun）
```

- `src/cli.ts`: `npx conduit-relay`のCLIエントリポイント（ビルド後は`dist/cli.js`）。`loader.ts`の`getBindingPath()`で解決した拡張子なし実行体（`native/cli.c`をビルドしたもの）を`spawn`するだけの薄いランチャー。`CONFIG_FILE`環境変数とSIGHUP/SIGTERM/SIGINTはこの実行体自身が処理する
- `src/index.ts`: ライブラリ利用向け。`loader.ts`の`getBinding()`で解決した`.node`アドオン（`native/napi_shim.c`をビルドしたもの）の生exportをそのまま`conduit`としてexport（シグナルハンドラは登録しない、ビルド後は`dist/index.js`）
- `src/loader.ts`: `.node`アドオン（`getBinding()`、require用）と拡張子なし実行体（`getBindingPath()`、spawn用）それぞれのパスをプラットフォーム別に解決する。ローカル`dist/`優先、無ければ`@conduit-relay/conduit-${platform}-${arch}`optionalDependencyへフォールバック
- `native/napi_shim.c`・`native/cli.c`: pure C（Goを含まない）。`libconduitcore.so`（`cmd/conduit/main_core.go`のビルド成果物）を`-Wl,-rpath,$ORIGIN`で動的リンクする。1プロセスに埋め込まれるGoランタイムを常に1つに保つための設計（詳細は`docs/adr/0009`）
- `scripts/build.ts`: `go build -tags=core -buildmode=c-shared`での`libconduitcore.so`ビルド→（`tsc`によるTSコンパイル、`gcc`での`napi_shim.c`/`cli.c`ビルドを並列実行）→ビルド時のみ必要な`libconduitcore.h`の削除、を行う
- Node.js v22以上が必要（Discord GatewayへのoutgoingWS接続にブラウザ互換のグローバル`WebSocket`を使うため）
- 設計判断の詳細は `docs/adr/0006`〜`0008`（Superseded）・`0009`を参照

## example/sample-worker（TypeScript / Cloudflare Workers）

```bash
cd example/sample-worker

bun dev --live-reload   # ローカル開発（ポート8787）
bun deploy              # Cloudflareへデプロイ
bun run lint            # Biomeによるlint
bun run cf-typegen      # wrangler.jsoncの変更後にbindingの型を再生成
```

bindings（`wrangler.jsonc`）を変更したら必ず `bun run cf-typegen` を実行して `worker-configuration.d.ts` を更新する。

## 設定ファイル

`example/config.sample.jsonc` がBotリスト設定のサンプル。

```json
{
  "id": "my-bot",
  "token": "Bot ...",
  "status": "active",
  "worker_ws_url": "wss://my-worker.workers.dev/gateway",
  "intents": 33281
}
```

- `status: "paused"` にするとconduitがそのBotへの接続を切断するが設定は残る
- `intents` はGateway Intentsのビットフィールド（例: GUILDS=1, GUILD_MESSAGES=512, MESSAGE_CONTENT=32768）
- MESSAGE_CONTENTインテント（32768）はDiscord Developer Portalで特権インテントを有効化が必要

## Cloudflare Workers

**Cloudflare Workers / KV / R2 / D1 / Durable ObjectsのAPIや制限は変わりやすい。作業前に公式ドキュメントを確認する。**

- Workers全般: https://developers.cloudflare.com/workers/
- Durable Objectsベストプラクティス: https://developers.cloudflare.com/durable-objects/best-practices/rules-of-durable-objects/
- 制限・クォータ: 各製品の `/platform/limits/` ページ（例: `/workers/platform/limits/`）
- エラー1102（CPU/Memoryオーバー）はWorkerの制限超過
