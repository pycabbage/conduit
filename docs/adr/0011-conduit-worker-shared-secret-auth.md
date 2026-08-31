---
title: "共有シークレットによるconduit⇔Worker間WebSocket認証"
date: "2026-09-01"
status: "Accepted"
---

# 0011. 共有シークレットによるconduit⇔Worker間WebSocket認証

## Context

`example/sample-worker/src/index.ts`の`/gateway`エンドポイントは現状、接続元を
検証していない。`fetch()`はリクエストを無条件でDO stubに委譲し、DOの
`webSocketMessage`は0005で決めた仕様どおり、`init`メッセージを受け取ると
無条件で`token`を`ctx.storage`に保存する。

この2つを組み合わせると、第三者が`wss://.../gateway`に直接WS接続し
`{"type":"init","token":"..."}`を送るだけで、DOが保持するBot tokenを
任意の値にすり替えられる。すり替え後は0005のフォールバック経路
（`this.token ?? await this.ctx.storage.get("token")`）によって偽token
がDiscord REST API呼び出しに使われ続けるため、正規のconduit接続がなくても
攻撃が成立する。

認証方式として以下を検討した。

- **共有シークレットによるヘッダー認証**：Worker Secrets（`CONDUIT_SECRET`）と
  conduit側`config.json`に同じ値を持たせ、WS Upgradeリクエストの
  `Authorization`ヘッダーで照合する。`github.com/coder/websocket`の
  `DialOptions.HTTPHeader`でUpgradeリクエストに任意ヘッダーを付与できることを
  確認済み
- **署名付きトークン（JWT等）**：有効期限や失効の仕組みを持てるが、
  1リレー・少数Bot運用の現状規模に対して鍵管理・検証ロジックの実装コストが
  見合わない
- **mTLS**：Cloudflare Workers側でクライアント証明書検証を組み込む構成が
  必要になり、DOへの薄いルーターというWorker設計（0002）に対して過剰

現状の運用規模（conduitインスタンス1つが複数Botを既知のWorker URLに接続する
構成）では、固定の共有シークレットを検証するだけで十分と判断し、共有シークレット
方式を採用する。

## Decision

### Worker側

- Cloudflare Secretsに`CONDUIT_SECRET`という名前で共有シークレットを登録する
  （`wrangler secret put CONDUIT_SECRET`）
- `fetch()`ハンドラで、`/gateway`パスをDO stubに委譲する**前**に
  `Authorization`ヘッダーを`env.CONDUIT_SECRET`と比較する
- ヘッダーが`Bearer <CONDUIT_SECRET>`と一致しない場合（欠落・不一致いずれも）は
  `401 Unauthorized`を返し、DOへの委譲を行わない

WorkerレベルでUnauthorizedを弾くことで、不正リクエストによってDOが起動する
ことすら防げる。DOのDuration課金はイベント処理時のみ発生させたいという0002の
「Workerは薄いルーター」方針とも整合し、認証失敗リクエストのために不要な
DOインスタンスを起動させないという意味でもこの方針に沿っている。

### conduit側

- `BotConfig`（`internal/relay/relay.go`）に`WorkerSecret string`
  フィールドを追加する。JSON keyは`worker_secret`
- `config.json`の各Botエントリに、対応するWorkerに設定した
  `CONDUIT_SECRET`と同じ値を`worker_secret`として設定する
- Workerへの`websocket.Dial`呼び出し（`runOnce`内、現状`nil`を渡している
  Dial）で、`DialOptions.HTTPHeader`に
  `Authorization: Bearer <cfg.WorkerSecret>`を設定してUpgradeリクエストに
  含める

### 比較方式について

Worker側の検証は単純な文字列一致（`===`）で行う。タイミング攻撃対策として
`crypto.subtle`によるHMAC比較などの定数時間比較を採用することも検討したが、
今回は採用しない。理由は以下の通り。

- 攻撃者が`CONDUIT_SECRET`を推測する主な手段はタイミングサイドチャネルでは
  なく、シークレットの漏洩（設定ファイルの流出、ログ出力等）である
- Cloudflare Workersの実行環境はネットワーク経由の応答時間に無視できない
  ジッターがあり、`===`比較1回分（数十バイトの文字列）の処理時間差を
  リモートから安定して観測することは実用上極めて困難
- 定数時間比較の実装・保守コストに対して、今回想定する脅威モデル
  （第三者による無差別なWS接続・token上書き）に対する追加の防御効果は
  見合わない

以上より、単純な文字列一致で十分と判断する。

## Consequences

- Worker側の事前設定にsecret登録（`wrangler secret put CONDUIT_SECRET`）が
  1つ増える。0005が目指した「Worker側の事前設定不要」という方針からは
  部分的に後退するが、認証を実現するために必要なトレードオフとして許容する
- 第三者による`/gateway`への不正接続・token上書き攻撃を防止できる。
  未認証リクエストはDOを起動させる前にWorkerレベルで`401`となるため、
  不正リクエストによるDO Duration課金も発生しない
- シークレットローテーション時は、Worker側の`wrangler secret put`と
  conduit側`config.json`の`worker_secret`の両方を更新する必要がある。
  更新順序を誤ると（Worker側だけ先に更新する等）その間conduitが
  再接続に失敗し続けるため、ロールアウト時は両者の値を揃えるタイミングに
  注意が必要
- シークレット自体が漏洩した場合、この認証機構は無効化される。この残存
  リスクは許容し、十分な長さのランダム文字列を使う運用を前提とする
