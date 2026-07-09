---
title: "wasm版はシグナルではなくJS関数でライフサイクル制御する"
date: "2026-07-08"
status: "Superseded by 0009"
---

# 0007. wasm版はシグナルではなくJS関数でライフサイクル制御する

## Context

0004で採用したSIGHUPによる設定再読込は、以下の理由によりwasmビルドでは使えない。

- `GOOS=js GOARCH=wasm`ターゲットには`syscall.SIGHUP`という定数自体が定義されて
  おらず、これをimportして参照すると`undefined: syscall.SIGHUP`というコンパイル
  エラーになる。
- 仮に`syscall.SIGTERM`のような定義済みの定数を使ったとしても、Goランタイムの
  js/wasm実装（`runtime/os_js.go`）にはシグナル配送を行う仕組みが一切存在せず、
  実行時にno-opになる。js/wasmポートの開発者自身も「シグナルを全くサポートしない」
  と明言している。

一方でCLIモード（`npx conduit-relay`）では、引き続きSIGHUPでの設定リロード、
SIGTERM/SIGINTでのgraceful stopをユーザーに提供したい。

## Decision

`cmd/conduit/main_js.go`はシグナル処理を一切持たず、`start`/`reload`/`stop`の
3関数を`syscall/js.FuncOf`でJSのグローバルにexportするだけにする。

Node.js側のCLIラッパー（`packages/conduit-relay/src/cli.ts`）が
`process.on('SIGHUP'/'SIGTERM'/'SIGINT')`でシグナルを受け取り、対応するexport
関数を呼び出すことで、ネイティブ版と同等のシグナル駆動の運用感を実現する。

| レイヤー | ライフサイクル制御 |
|---|---|
| ネイティブCLI（`cmd/conduit/main.go`） | `os/signal`でSIGHUP/SIGTERM/SIGINTを直接処理 |
| wasmモジュール（`cmd/conduit/main_js.go`） | シグナル処理なし。`start`/`reload`/`stop`をJSにexportするのみ |
| `packages/conduit-relay/src/cli.ts` | Node.jsのシグナルを受け取り、export関数を呼び出す |
| `packages/conduit-relay/src/index.ts` | シグナルリスナーなし。呼び出し元が明示的に呼ぶ |

## Consequences

- ライブラリとして`packages/conduit-relay/src/index.ts`（`start`/`stop`/`reload`
  のみexport）を直接importして使う場合はシグナルリスナーが一切登録されないため、
  利用者はJS APIから明示的に`reload`/`stop`を呼ぶ設計に自然になる。
- SIGHUPドリブンの挙動が欲しい場合のみCLIエントリポイント（`dist/cli.js`、
  `npx conduit-relay`）経由で使う、という2モードの使い分けが生まれた。
- wasmモジュール自体はライフサイクル制御の手段（シグナル or JS関数呼び出し）に
  ついて何も判断しない、薄いexportレイヤーのままになる。
