---
title: "conduit-relay npmパッケージの実装方式"
date: "2026-07-08"
status: "Accepted"
---

# 0008. conduit-relay npmパッケージの実装方式

## Context

Go公式が生成する`wasm_exec_node.js`（Node.js向けブートストラップ）は、「argvを
実行し、Goプログラム終了と共にプロセスも終了する」一回限りのCLIツール実行を
前提とした設計になっている。これは`start`/`stop`/`reload`で繰り返し・
インタラクティブに制御したい今回の常駐ライブラリ用途と噛み合わないため採用しなかった。

パッケージの実装言語・レイアウトとしてTypeScript・ESM・`src/`レイアウトを採用した。

## Decision

自前の`packages/conduit-relay/src/load.ts`を書き、Go公式の`wasm_exec.js`本体
（ホスト非依存のGo↔JSブリッジ部分）はそのまま流用しつつ、Node.js向けに必要な
最小限のグローバル（`fs`, `path`）だけを注入する設計にした。

`wasm_exec.js`および`conduit.wasm`はソースコードの一部ではなく、ビルド時
（`scripts/build.sh`）に生成・配置されるビルド成果物である。`src/load.ts`は
これらを`new URL('./wasm_exec.js', import.meta.url)`のような実行時の
computed URLで解決している。これはビルド成果物への参照をソースコード上の
静的な相対importとして書けない（ビルド前は実体が存在しない）ための設計であり、
実行時に`dist/`ディレクトリ内の隣接ファイルとして解決される。

ビルドは`scripts/build.sh`で行う。

1. `bun tsc`で`src/`配下のTypeScriptを`dist/`へコンパイル
2. `GOOS=js GOARCH=wasm go build -o packages/conduit-relay/dist/conduit.wasm ./cmd/conduit`
3. `$(go env GOROOT)/lib/wasm/wasm_exec.js`を`dist/wasm_exec.js`へvendoring

`loadRelay()`は`go.run(instance)`の完了を待たない。`cmd/conduit/main_js.go`の
`main()`は`select {}`で永久にブロックし続ける設計（0006参照）のため、
`go.run(instance)`が返すPromiseは正常系では解決せず、Go側の予期しないpanic/exit
のときのみ解決する。そのため`loadRelay()`は代わりに、`main()`が`start`/`reload`/
`stop`を登録し終えたことを示す`__conduitReady`グローバルを一定時間（10秒）
ポーリングして待つ。この待機はGoのバージョン間でのスケジューリング詳細の変化に
対する防御であり、通常は`go.run(instance)`呼び出し中に同期的に完了する。

## Consequences

- `packages/conduit-relay/dist/`はビルド成果物でgit管理外とし、パッケージを
  使う前に必ず`bash scripts/build.sh`の実行が必要。
- `tsc`はファイル単位でのトランスパイルのみを行いバンドルはしないため、`dist/`には
  `src/`の各ファイルに対応する個別の`.js`/`.d.ts`が生成される
  （例: `src/load.ts` → `dist/load.js`）。
- `GOOS=js GOARCH=wasm`ビルドの成功（約5.7MB）、およびNode.js互換ランタイム
  （bun）上で実際にwasmモジュールをロードし`start`/`stop`/`reload`が呼び出し
  可能であることは実機確認済み。ただし実際にDiscord Gateway/Cloudflare Worker
  への接続が成功することまでは未検証。
