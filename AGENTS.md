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

- **main.go** 一枚。Bot設定ファイルを読んで各BotのgoroutineをSIGHUPトリガーで差分管理する
- **example/sample-worker/src/index.ts** 一枚。Worker（薄いWSルーター）とConduitDO（Hibernation APIサーバー）を同居させたサンプル
- WS接続確立直後にconduitがinitメッセージ（`{"type":"init","token":"Bot ..."}`）を送りDOが`ctx.storage`に保存する（事前設定不要）

設計判断の根拠は `docs/adr/` を参照。

## conduit（Go）

```bash
# ビルド
go build .

# ローカル実行（設定ファイルを指定）
CONFIG_FILE=./example/config.sample.jsonc go run .

# Dockerイメージビルド（リポジトリルートから実行）
docker build -t conduit .

# 設定再読み込み（ホットリロード）
kill -HUP <pid>
```

`main.go` の依存パッケージは `github.com/coder/websocket` のみ。

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
