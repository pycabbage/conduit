---
title: "Bot tokenをWS initメッセージでDOに渡す"
date: "2026-06-28"
status: "Accepted"
---

# 0005. Bot tokenをWS initメッセージでDOに渡す

## Context

DOがDiscord REST APIを呼ぶためにBot tokenが必要。
Worker側にtokenを事前設定する方法として以下を検討した。

- **Worker環境変数（`.dev.vars` / Cloudflare secret）**：全DOが同一tokenを使う前提になる。Workerのデプロイにtokenが依存する
- **WSのinitメッセージで渡す**：tokenはBot設定ファイルにあるのでリレーが保持している。WS接続確立時に送ればWorker側の設定が不要になる

## Decision

リレーがWorkerへのWS接続を確立した直後、最初のメッセージとして以下を送信する。

```json
{"type": "init", "token": "Bot ..."}
```

DOはこれを受け取り`ctx.storage.put("token", token)`で永続化する。
Hibernate後の再起動時は`ctx.storage.get("token")`でフォールバック取得する。

`zInitMessage`スキーマでinitメッセージとDiscord Gatewayペイロードを区別する。

## Consequences

- Worker側にBot tokenの事前設定が不要（`.dev.vars`やCloudflare secretへの登録が不要）
- 将来1つのWorkerに複数BotのDOを同居させる場合も、各DOが独立してtokenを保持できる
- リレー再接続のたびにinitメッセージを再送するためDOのstorageは常に最新のtokenで上書きされる
