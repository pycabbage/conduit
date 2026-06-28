---
title: "リレー実装言語にGoを採用する"
date: "2026-05-19"
status: "Accepted"
---

# 0003. リレー実装言語にGoを採用する

## Context

リレーの用途はIO-bound（WebSocket待ち受けと転送のみ）で、複数BotのWS接続を同時に維持する必要がある。
デプロイ先のVMはliteインスタンス相当（256MB RAM前後）を想定しており、メモリ効率が重要。

候補として以下を検討した。

| 言語 | 起動時ベースメモリ | WS接続あたりの追加メモリ | 備考 |
|---|---|---|---|
| Go | 数MB〜十数MB | 数十KB | goroutineスタック初期4KB、Dockerイメージ数MB |
| Rust | 数MB以下 | 数十KB以下 | コンパイル時間が長く開発イテレーションが遅い |
| Elixir | 数十〜100MB前後 | 数百bytes | BEAM VM自体のベースメモリが重い |

## Decision

リレーの実装言語にGoを採用する。

- goroutineはスタック初期4KBから動的拡張するため、Bot数分のWS接続を並行管理してもメモリ消費が小さい
- `github.com/coder/websocket`でWS実装が簡潔に書ける
- `scratch`または`alpine`ベースでDockerイメージを数MBに抑えられる
- IO-boundの並行処理はgoroutine+channelで自然に書ける

## Consequences

- リレーはシングルバイナリとしてビルドでき、デプロイが単純
- Rustと比べてメモリフットプリントはやや大きいが、256MB枠での20〜30Bot同居は現実的
- Go 1.26.4を使用（`module github.com/pycabbage/conduit`）
