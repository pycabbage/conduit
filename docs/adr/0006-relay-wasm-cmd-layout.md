---
title: "conduitをWebAssembly化しnpmで配布する"
date: "2026-07-08"
status: "Accepted"
---

# 0006. conduitをWebAssembly化しnpmで配布する

## Context

conduitはGo製の常時稼働プロセスで、運用には専用VM相当のホストが必要になる。
Node.jsエコシステムに閉じた環境（既存のNode.jsプロセスマネージャ配下に置きたい、
Goツールチェインを用意したくない、等）ではこの前提がハードルになる。

conduitのコア機能は2本のWebSocket中継（Discord Gateway、Worker）であり、一見
`net.Dial`によるTCPソケットに依存しているように見える。しかし依存ライブラリ
`github.com/coder/websocket`には`ws_js.go`というファイルがあり、Goのビルドタグ
規約（`_js.go`サフィックス）により`GOOS=js`の場合のみコンパイルされる。この実装は
`syscall/js`経由でホスト（Node.js）が提供するグローバルの`WebSocket`オブジェクトを
ラップしており、TCPソケットには一切依存しない。これによりconduitのコア機能は
`GOOS=js GOARCH=wasm`でも成立する見込みが立った。

WebAssemblyターゲットとして以下を検討した。

| ターゲット | 評価 |
|---|---|
| `GOOS=js GOARCH=wasm`（標準Goツールチェイン） | `ws_js.go`がホストの`WebSocket`グローバルをラップしており成立する |
| `GOOS=wasip1 GOARCH=wasm`（WASI Preview1） | ソケット生成の仕組みが仕様レベルで存在せず、Node.jsの`node:wasi`もソケット非対応のためdead end |
| TinyGo | `reflect`パッケージの制限やビルドサイズ縮小効果が本ケースでは不確実なため見送り |

## Decision

標準Goツールチェインで`GOOS=js GOARCH=wasm go build`し、conduitをwasmバイナリとして
ビルドする（実機確認: 約5.7MB）。

- プラットフォーム非依存のロジック（`BotConfig`, `Manager`, `runOnce`, `botRun`,
  `discordWrite`）を`internal/relay`パッケージへ抽出し、ネイティブCLI
  （`cmd/conduit/main.go`, `//go:build !js`）とWASM
  （`cmd/conduit/main_js.go`, `//go:build js`）の両エントリポイントから共有する。
- `main.go`と`main_js.go`は別ディレクトリに分割せず、`cmd/conduit/`という同一
  ディレクトリに同居させ、ビルドタグで排他させる。別ディレクトリ
  （例: `cmd/conduit-wasm/`）に分けると、ネイティブGOOS向けビルド時にそのディレクトリが
  「ビルド対象ファイル0個のパッケージ」になり、goreleaserのビルドが壊れるリスクがある。
  同一ディレクトリであれば、どのGOOSでビルドしても常に有効なmainパッケージが1つ存在する。
- `internal/relay.Manager`に`sync.Mutex`を追加する。元のシングルファイル実装
  （削除済みの旧`main.go`）は設定適用ロジック（`applyConfigs`相当）を単一goroutine
  （シグナル処理ループ）からしか呼んでいなかったため排他制御が不要だったが、wasm版では
  `start`/`reload`/`stop`がJSの`syscall/js.FuncOf`コールバックから並行に呼ばれる
  可能性があるため、`running`マップを保護する排他制御が必要になった。
- `start`は呼ばれるたびに新しい`Manager`を作り直す。直前の`Manager`が存在する場合は
  それを`StopAll`してから差し替えることで、`stop`を挟まずに`start`を連続で呼んでも
  古いBot goroutineが孤立して残り続けることがないようにしている。
- `main()`は全ての`js.Func`を登録した後、`select {}`で永久にブロックする。js/wasmの
  ランタイム実装（`runtime/lock_js.go`の`beforeIdle`）は、実行可能なgoroutineが
  尽きるたびに新しいイベントハンドラgoroutineを自動生成してJSのイベントループへ制御を
  戻す設計になっており、`select {}`単体で待機してもGoの標準的なdeadlock検出
  （`checkdead`、"all goroutines are asleep"）には引っかからないことをランタイム
  ソースを読んで確認済み。追加のkeep-aliveタイマー等は不要。

## Consequences

- `internal/relay`がネイティブ・wasm両エントリポイントで共有される単一の実装になり、
  リレーロジックの二重実装を避けられる。
- Managerがmutexを持つことで、ネイティブ版でも（元々不要だった）排他制御コストが
  わずかに発生するが、単一goroutineからの呼び出しが大半のため実質的な影響はない。
- `main_js.go`がエクスポートする`start`/`reload`/`stop`の`js.Func`値は意図的に
  `Release()`しない。これらはプロセス生存期間中ずっとJSから呼び出し可能である必要が
  あり、`Release()`すると以後の呼び出しが無効になるため。
- `GOOS=js GOARCH=wasm`ビルドの成功と、Node.js互換ランタイム上でのwasmモジュール
  ロード・関数呼び出しは実機確認済みだが、実際のDiscord Gateway/Cloudflare Worker
  への接続成功は未検証（詳細は0008を参照）。
- WASIやTinyGoは、将来ビルドサイズや依存関係が問題になった場合の再検討候補として残る。
