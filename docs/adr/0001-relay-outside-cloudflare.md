---
title: "Discord Gatewayへの接続をCloudflare外VMで行う"
date: "2026-05-19"
status: "Accepted"
---

# 0001. Discord GatewayへのWS接続をCloudflare外VMで行う

## Context

Discord GatewayへのWebSocket接続はBot側がクライアント（Outgoing WS）になる。
Cloudflareプラットフォームには以下の制約がある。

- Cloudflare ContainersからはDiscord側のIPブロックにより`wss://gateway.discord.gg`への接続が401で弾かれる
- Durable ObjectsのHibernation APIはIncoming WS（DOがサーバー側）にのみ対応しており、Outgoing WSでは機能しない
- Outgoing WSを維持中のDOはHibernationに入れないためDuration課金が常時発生する

これらの制約から、Cloudflareプラットフォームだけでは常時接続とHibernationを両立できない。

## Decision

Discord Gatewayへの接続を担うリレーをCloudflare外のVMで動かす。

候補として以下を検討した。

| プラットフォーム | 評価 |
|---|---|
| GCP Cloud Run | WebSocket接続がリクエスト扱いのため最長60分で強制切断 |
| Fly.io | 2024年に無料枠廃止、実質$5/月〜 |
| Railway | トライアルクレジット消化後は有料 |
| GCP Compute Engine e2-micro | Always Free永続枠あり、VMなのでWS制限なし |
| Oracle Cloud Always Free | AMD Micro×2またはAmpere A1 4OCPU/24GB、Egress 10TB/月 |

VMであればタイムアウト制限がなくWebSocket長期接続を維持できる。

## Consequences

- リレーVMは常時稼働が必要であり固定コストが発生する（GCP e2-micro Always Free使用時は$0の可能性あり）
- Cloudflare側のコストをWorkers Freeプラン範囲に抑えられる可能性がある（Paid plan不要）
- リレー再起動時に全Gateway接続が切断される（Session Resumeで影響を軽減）
