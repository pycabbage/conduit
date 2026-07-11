---
title: "npmパッケージ配布(conduit-relay)を廃止し、Goネイティブバイナリのみに一本化する"
date: "2026-07-12"
status: "Accepted"
---

# 0010. npmパッケージ配布(conduit-relay)を廃止し、Goネイティブバイナリのみに一本化する

## Context

0006〜0009では、conduitのコアリレーロジックをNode.js/Bunエコシステム向けに
`conduit-relay`というnpmパッケージとして配布する設計を積み上げてきた。

- 0006・0007: `GOOS=js GOARCH=wasm`によるwasmビルドとそのライフサイクル設計。
- 0008: `conduit-relay`npmパッケージそのものの導入。
- 0009: wasm版を廃止し、native Node-APIアドオン（`.node`）＋pure Cの
  シム（`napi_shim.c`/`cli.c`）＋Go製の共有ライブラリ（`libconduitcore.so`、
  `cmd/conduit/main_core.go`）という3成果物構成に置き換え。

しかし0009時点で、この設計は複数の未検証事項を残したままだった（0009
「起床後に確認すべきこと」節を参照）。

- Node.js本体での動作確認が行えていない（本開発環境ではBunでの検証のみ）。
- 実際のDiscord Gateway/Cloudflare WorkerへのWebSocket接続が、native
  アドオン内の`internal/relay`のgoroutineパスで成立するかが未検証
  （本開発環境はネットワーク到達性の確認自体ができない）。
- `native/cli.c`のシグナル処理が、SIGHUP連続配送のような現実的な
  シナリオに耐えるかも未再検証のまま。

この状態で、npmパッケージとしての配布自体を取りやめる方針転換が決まった。
conduitのデプロイ先は「Cloudflare外VMで常時稼働」という単一のGoネイティブ
バイナリ運用が前提であり、上記の未検証事項を1つずつ潰してNode.js/Bun
エコシステム向けの配布を維持するコストは、この前提に見合わないと判断した。

## Decision

`conduit-relay`npmパッケージの配布を廃止し、conduitはGoネイティブバイナリ
（`cmd/conduit/main.go`、goreleaser/Dockerによる配布）のみに一本化する。

具体的に行った変更:

- `packages/conduit-relay/`ディレクトリを削除した。TypeScriptソース
  （`src/index.ts`, `src/cli.ts`, `src/loader.ts`等）、ビルドスクリプト
  （`scripts/build.ts`）、pure Cのネイティブグルー
  （`native/napi_shim.c`, `native/cli.c`）を含む一式が対象。
- `cmd/conduit/main_core.go`（npmパッケージ向けのC ABIエクスポート、
  `ConduitStart`/`ConduitStop`/`ConduitReload`を`//export`していた
  `//go:build core`ファイル）を削除した。
- `cmd/conduit/`にビルドタグ付きファイルが`main.go`1つだけになったため、
  `main.go`の`//go:build !core`制約を除去した。
- `README.md`/`AGENTS.md`からnpm/npx関連の記述を削除した。
- `.gitignore`の`*.node`エントリを削除した。

## Consequences

- `internal/relay`・`cmd/conduit/main.go`はこの変更で無改修であり、
  ネイティブCLIの挙動・ビルド（`go build ./cmd/conduit`、goreleaser、
  Docker）に影響がない。npmパッケージ向けの成果物（`main_core.go`、
  `packages/conduit-relay/native/`のpure Cシム）だけが対象の変更であり、
  Botリレーの中核ロジックは常にネイティブCLI経路のみを通っていたため、
  挙動分岐のリスクはそもそも存在しない。
- Node.js/Bunエコシステム向けの配布手段（`npx conduit-relay`でのCLI起動、
  `import { conduit } from "conduit-relay"`でのライブラリ利用）が無くなる。
- 将来、Node.js/Bun向け配布が再度必要になった場合は、0006〜0009に
  wasm版・native Node-APIアドオン版それぞれの実装の経緯と実機検証結果
  （residency確認、dlopenとexecの両立不可問題、C文字列所有権契約等）が
  残っているため、そこから再構築できる。
- `example/sample-worker`（Cloudflare Workersのサンプル、bunで開発）は
  npmパッケージ配布とは無関係（Cloudflare Worker側のコードであり、
  `conduit-relay`パッケージに依存していない）のため、この変更による
  影響を受けない。
