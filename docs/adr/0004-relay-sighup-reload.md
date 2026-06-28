---
title: "リレーの設定再読み込みにSIGHUPを使用する"
date: "2026-05-19"
status: "Accepted"
---

# 0004. リレーの設定再読み込みにSIGHUPを使用する

## Context

Bot追加・削除・停止のたびにリレーを再デプロイしたくない。
リレーはVMローカルの設定ファイル（`/etc/conduit/config.json`）を読み込んで起動する。

設定変更をリレーに反映する方法として以下を検討した。

- **定期ポーリング**（30秒ごとにファイル再読み込み）：不要な複雑性。設定変更のタイミングは運用者が決めるものであり、リレーが勝手に読み直す必要はない
- **SIGHUP再読み込み**：UNIXの標準的な慣習。`kill -HUP <pid>`で即時反映できる

## Decision

SIGHUPシグナルを受信したときに設定ファイルを再読み込みする。

差分に応じて個別のBot goroutineをstart/stopし、他のBotへの影響はない。
SIGTERMおよびSIGINT受信時はグレースフルシャットダウンする。

| イベント | リレーの動作 |
|---|---|
| Bot追加 | 新しいgoroutineをspawn |
| Bot削除・停止 | 該当goroutineをcancel |
| SIGHUP | 設定再読み込みして差分適用 |
| SIGTERM/SIGINT | 全goroutineをcancelして終了 |

## Consequences

- Bot管理の操作（追加・削除・停止）はリレーの再デプロイ不要
- 設定変更のタイミングを運用者が完全にコントロールできる
- `CONFIG_FILE`環境変数でパスを指定（デフォルト`/etc/conduit/config.json`）
