---
title: "Worker+DO Hibernation APIでBotロジックをオフロードする"
date: "2026-05-19"
status: "Accepted"
---

# 0002. Worker+DO Hibernation APIでBotロジックをオフロードする

## Context

リレー（Cloudflare外VM）がDiscord GatewayイベントをWorkerに渡す方法として複数の選択肢がある。

- **HTTP転送**（Outbound Handler経由）：リクエストごとにWorkerが起動するが、双方向通信が難しい
- **WS転送→DO Hibernation**：リレーがWorkerへのWS接続を確立しDOがサーバーになることでHibernation APIが利用できる

DOがIncoming WSサーバーとして動作する場合、Hibernation APIが機能する。
リレーはWSクライアントとしてDOに接続するため、DO側はIncoming WSとして受け付けられる。

## Decision

以下のフローでBotロジックをオフロードする。

```
[初回接続]
conduit → HTTP Upgrade → Worker.fetch()
                         → doStub.fetch() → DO.fetch()
                             → ctx.acceptWebSocket(server)
                         ← 101 Switching Protocols

[イベント受信時]
Discord Gateway → conduit → ws.send(payload) → DO.webSocketMessage()
                                               → 処理
                                               → Hibernate
```

- Workerのfetchハンドラは薄いルーターとして機能しDO stubに委譲して即終了する
- DOはHibernation APIを使用し、イベント処理時のみ起動する
- conduitからのWebSocket pingはCloudflare runtimeが自動応答するためDOを起こさない

## Consequences

- DOのDuration課金はイベント処理時（数十ms）のみ発生し、待機中は実質$0
- Workerの実行時間はHTTP Upgradeハンドリングのみで最小化される
- conduit→DO間の接続はconduitが再起動するたびに再確立が必要
