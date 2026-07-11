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

- Bot設定ファイルを読んで各BotのgoroutineをSIGHUP（またはJS API経由）トリガーで差分管理する。中核ロジックは `internal/relay`（`Manager`, `BotConfig`, Discord Gateway ↔ Worker中継ループ）に切り出されており、プラットフォーム固有のエントリポイントは `cmd/conduit/` に同居し、それぞれビルドタグで分離されている（native/js向けを別ディレクトリに分けると、片方のGOOS向けビルド時にもう一方のディレクトリが「ビルド対象ファイル0個のパッケージ」になりgoreleaserのビルドが壊れるため、同一ディレクトリに置く設計）:
  - `cmd/conduit/main.go`（`//go:build !js`）: ネイティブCLI。`os/signal`でSIGHUP/SIGTERM/SIGINTを処理
  - `cmd/conduit/main_js.go`（`//go:build js`）: `GOOS=js GOARCH=wasm`向け。`syscall/js`で`start`/`reload`/`stop`をグローバル関数としてエクスポートし、`select{}`で常駐する（js/wasmにはシグナル配送機構がないため`os/signal`は使わない）
- **`packages/conduit-relay/`**: 上記wasmビルドをNode.js向けnpmパッケージとして配布。`npx conduit-relay`でのCLI起動と、`start`/`stop`/`reload`関数のライブラリ利用の両方に対応（詳細は `packages/conduit-relay/README.md`）
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

# wasmビルド確認（GOOS=js GOARCH=wasm）
GOOS=js GOARCH=wasm go build ./cmd/conduit

# Dockerイメージビルド（リポジトリルートから実行）
docker build -t conduit .

# 設定再読み込み（ホットリロード）
kill -HUP <pid>
```

依存パッケージは `github.com/coder/websocket` のみ（`internal/relay`・`cmd/conduit/main.go`・`cmd/conduit/main_js.go`共通）。

## packages/conduit-relay（Node.js / npm）

TypeScript・ESM・`src/`レイアウト。ビルドは`scripts/build.sh`（シェルスクリプト）。

```bash
cd packages/conduit-relay

bash scripts/build.sh   # dist/conduit.wasm, dist/wasm_exec.js, dist/*.js, dist/*.d.ts を生成（要ローカルGoツールチェインとBun）
```

- `src/cli.ts`: `npx conduit-relay`のCLIエントリポイント（ビルド後は`dist/cli.js`）。`CONFIG_FILE`環境変数とSIGHUP/SIGTERM/SIGINTがネイティブ版と同じ挙動
- `src/index.ts`: ライブラリ利用向け。`start`/`stop`/`reload`をexport（シグナルハンドラは登録しない、ビルド後は`dist/index.js`）
- `src/load.ts`: Node向け自前wasmローダー（stockの`wasm_exec_node.js`は常駐用途と噛み合わないため使用しない）。`wasm_exec.js`/`conduit.wasm`はビルド成果物でソースに存在しないため、`new URL(...)`による実行時のcomputed URL経由で解決する
- `scripts/build.sh`: `bun tsc`によるTSコンパイル→`GOOS=js GOARCH=wasm go build ./cmd/conduit`でのwasmビルド→`wasm_exec.js`のvendoringを行う
- Node.js v22以上が必要（Discord GatewayへのoutgoingWS接続にブラウザ互換のグローバル`WebSocket`を使うため）
- 設計判断の詳細は `docs/adr/0006`〜`0008`を参照

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
