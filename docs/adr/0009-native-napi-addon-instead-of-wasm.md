---
title: "conduit-relayをwasmではなくnative Node-APIアドオンとして実装する"
date: "2026-07-09"
status: "Accepted"
---

# 0009. conduit-relayをwasmではなくnative Node-APIアドオンとして実装する

## Context

0006〜0008で採用した`GOOS=js GOARCH=wasm`版は実際に動作確認まで到達したが、
以下の理由で置き換えることにした。

- Goのjs/wasmランタイム（`syscall/js`）は`js.Global()`、すなわちJSの
  `globalThis`を経由してGo↔JSブリッジを行う設計になっている。TS側の
  wasmローダー（`load.ts`の`callGo`相当）は`globalThis[name]`でGoが
  `js.Global().Set(...)`によりexportした関数を読み出すしかなく、この
  依存を設計変更なしに除去できない。
- ユーザーからTS側で`globalThis`/`global`/`self`を一切使わない実装を
  求められた。またJS APIは`const relay = await start(config); await
  relay.stop()`のような、ハンドルオブジェクトを返す形を求められた。

この2つの要求を満たすため、以下の4方式を実機検証・比較した。

| 方式 | globalThis依存 | 実装コスト | 配布形態 | ランタイム制約 |
|---|---|---|---|---|
| `GOOS=js`（0006〜0008、既存） | `callGo`に残る（除去不能） | なし（動作確認済み） | 単一の可搬wasm、`Bun.build()`のpluginでbase64インライン化すればBun専用API不使用の移植可能なJSも生成可能（実機確認済み） | Node.js/Bun等、任意 |
| `GOOS=wasip1`（reactorモデル、`-buildmode=c-shared`） | 排除可能（`instance.exports.xxx()`直呼び） | 非常に高い（後述） | 単一の可搬wasm | 任意 |
| native + `bun:ffi`（`-buildmode=c-shared`のネイティブ共有ライブラリ） | 排除可能 | 最小（既存ロジックほぼ無変更） | プラットフォーム別`.so`/`.dylib`/`.dll` | **Bun専用**（`bun:ffi`はBun固有API） |
| native + Node-API（`.node`、本ADRの採用案） | 排除可能 | 最小（既存ロジックほぼ無変更） | プラットフォーム別`.node` | Node.js/Bun両対応（後述の限界あり） |

### wasip1を選ばなかった理由（実機検証済み）

`GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared`でreactorモデル
（`_initialize`export）のビルドは成立し、手書きの14-import WASIスタブで
（BunのNode.js互換`node:wasi`実装は未成熟で、最小構成のGoバイナリでも
`wasi.start()`が"Out of bounds memory access"を起こすため、これを迂回する
形で）`_initialize()`や追加exportの直接呼び出しは動作した。

しかし決定的な問題は、**goroutineがexport呼び出しの実行中にしか進行しない**
ことである。バックグラウンドgoroutine（`time.Sleep`ループ）を起動した状態で
`ping()`を5回呼んでも`1,2,3,4,5`にしかならず（=待機中は完全に停止）、
`ping()`内で`runtime.Gosched()`を1000回呼んだ場合のみ`3,104,205`と進行した。
これはGOOS=jsの`beforeIdle`（`runtime/lock_js.go`）に相当する、ホストの
アイドル時間とGoランタイムを協調させる仕組みがwasip1のreactorモデルには
存在しないことを意味する。

したがってwasip1でconduitを成立させるには、Discord Gateway/WorkerのWebSocket
受信、heartbeat送信、再接続バックオフなど、`internal/relay`が現在
「goroutineでブロッキングI/Oを回す」設計に依存している全ての処理を、
「JS側が一定間隔・イベント契機でGoのexportを叩く」ホスト駆動モデルへ
全面書き換えする必要がある。加えてwasip1にはソケット拡張がないため
（`wasi:sockets`はwasip2の話であり、後述の理由で採用しなかった）、
WebSocket通信自体も`//go:wasmimport`でJS側の`WebSocket`をGoから間接的に
操作する形で自前実装する必要がある。これらの代償は、後述のnative方式が
実質ゼロの書き換えで同じ目的を達成できることと比べて見合わない。

（wasip2 + TinyGoでの`wasi:sockets`利用も検討したが、これは
"Goがソケットを所有してblocking readする"構成になり、上と同じ
「ホストアイドル中はgoroutineが進行しない」問題を再導入するリスクが
高いため、深掘りせずに不採用とした。）

### native方式を選んだ理由（実機検証済み）

`-buildmode=c-shared`でネイティブの共有ライブラリとしてビルドしたGo
バイナリは、独立したOSスレッド上でGoランタイムが動作するため、
**残機能なしにresidency問題が発生しない**。実機で以下を確認した。

- `bun:ffi`経由でロードした場合、JS側が300ms/500ms/1000ms何もFFI呼び出しを
  しない間もバックグラウンドgoroutineが自律的に進行し続けた
  （`3 → 504 → 1505 → 2006`、期待値どおり）。
- `Start`/`Stop`を複数回呼ぶ再起動サイクル（reload相当）も、dlopen1回・
  ランタイム初期化1回のプロセス内で正しく動作した（世代ごとのgoroutineが
  正しく終了・再起動する）。
- 自前でcgo経由`node_api.h`をラップした最小実装（`napi_register_module_v1`
  を`//export`し、`napi_create_function`でGoのコールバックを登録）も同様に
  ビルド・Bunでのロード・residencyを確認した。外部の日本語圏外の小規模な
  第三者ライブラリ（`napi-go`等）への依存は、ユーザーの意向により採用しない。

native方式は、Goが`net.Dial`で本物のTCPソケットを所有できるため、
`github.com/coder/websocket`のTCP実装や`internal/relay`の
「goroutineでブロッキングReadを回す」設計を**無改修で流用できる**。
これはwasip1が払う代償（relay全面書き換え + WebSocket自前実装）を
一切払わずに、ユーザーの要求（globalThis不使用、ハンドル返却API）を
満たせることを意味する。

## Decision

conduit-relayのランタイム実装を、wasmビルドからNode-API準拠のネイティブ
アドオン（`.node`）に切り替える。

- 既存の`cmd/conduit/main.go`（`//go:build !js`）が持つ`relay.Manager`
  （`NewManager`/`Apply`/`StopAll`）と`internal/relay`パッケージは
  **無改修で再利用する**。これはリライトではなく、signal駆動の起動ループを
  Node-APIのexport関数に差し替えるラッパーを被せるだけの変更である。
- Node-APIバインディングは外部ライブラリに依存せず、`node_api.h`
  （Node-API公式ヘッダー）をcgoで直接インクルードし、
  `napi_register_module_v1`をエントリポイントとして自前実装する。
  必要な関数は`napi_create_function`, `napi_create_string_utf8`,
  `napi_get_value_string_utf8`, `napi_get_cb_info`,
  `napi_set_named_property`等、ごく少数に限定する。
  ヘッダーの入手は実装時に`node-api-headers`（npm、Node.js公式配布）等の
  再現可能な手段で解決し、探索時に使った`/nix/store/...`のような
  環境固有パスをビルド成果物や設定に残さない。
- ビルドタグでこのcgoターゲットを厳格に隔離する（例:
  `cmd/conduit-napi/`または新しいビルドタグ）。ネイティブCLI
  （`CGO_ENABLED=0`が前提の既存クロスコンパイル、goreleaser matrix）を
  汚染しないこと。
- 設定の受け渡しはcontent-basedにする。`index.ts`の`start(config)`は
  `object | string`を受け付け、オブジェクトは`JSON.stringify`してGo側に
  渡し、パース自体はGo側で行う。CLI（`cli.ts`）でのファイル読込は
  別入口として薄く保つ（詳細は実装フェーズのプランで確定する）。
- 配布はプラットフォーム別`.node`（linux-x64/arm64, darwin-x64/arm64,
  windows-x64等）を`optionalDependencies`で分割する、esbuild/sharp等と
  同様の定石に従う。cgoクロスコンパイルには`zig cc`等の利用を検討する。
- 0006〜0008のGOOS=js版（`load.ts`, `wasm_exec.js`, `wasm_exec.d.ts`,
  `conduit.wasm.d.ts`、`build.sh`のwasmビルドステップ等）は削除する。
  当初は「native方式がend-to-endで検証されるまでfallbackとして残す」と
  していたが、実装の結果このwasm版は「ビルドはされるがロードできない」
  壊れた状態になっており、fallbackとして機能していなかった。動作しない
  コードは後方互換にすらならず負債でしかないため、宙ぶらりんに残さず
  削除する（GOOS=js方式の詳細と経緯は0006〜0008およびgit履歴に残るため、
  将来再検討が必要になれば復元できる）。
- **v1ではGo→JSのコールバック/イベントブリッジを作らない。** `napi_env`/
  `napi_value`はJSメインスレッド専用であり、goroutineから別スレッドで
  直接NAPI関数を呼ぶと壊れる。安全にGo→JSを行うには`napi_threadsafe_function`
  が必要になるが、これは native 方式がせっかく回避したスレッド安全性の
  複雑さを再導入するうえ、本開発環境では動作検証ができない。幸いrelayは
  そもそもJS側にコールバックする必要がない設計にできる:
  Goが`net.Dial`で接続を全て所有するため、JSは`start`/`stop`/`reload`を
  呼ぶだけでよい。ログは標準出力（`fmt.Println`等）に任せ、エラーは
  `start`の同期的な戻り値で通知し、接続断からの再接続は`internal/relay`
  既存のロジックに任せる。
- **v1のスコープを次に限定する**: 既存`Manager`をcgo経由のNode-API
  ラッパーで公開し、`.node`をBunで`require`し、`start(configString)`/
  `stop()`/`reload(configString)`が実行できることを確認するところまで。
  `.node`側はflatな関数をexportするだけに留め、`{stop, reload}`を返す
  ハンドルオブジェクトはTS側（`index.ts`）で組み立てる（C/cgo面を必要
  最小限に保つため）。文字列引数の受け渡し（`napi_get_cb_info`→
  `napi_get_value_string_utf8`→Go string）は実機未検証のため、実装の
  最初の増分で確認する。
- **多プラットフォーム配布は本ADRのスコープ外とする。** conduitの
  デプロイ先は「Cloudflare外VMで常時稼働」という単一プラットフォーム
  （linux-x64）が前提であり、`optionalDependencies`によるプラットフォーム
  別`.node`分割は将来必要になった時点のfuture workとし、v1では対象
  プラットフォーム1つ向けのビルドのみを行う。

## Consequences

- `internal/relay`・`cmd/conduit/main.go`の書き換えがほぼ不要になり、
  wasip1移行で見込まれていた大規模な並行モデル書き換え・WebSocket自前実装
  の代償を払わずに済む。
- ネイティブアドオンのため、単一の可搬バイナリという wasm 版の利点は失われ、
  プラットフォームごとのビルド・配布が必要になる。`npx conduit-relay`を
  幅広い環境に配る前提であれば、このコストは実在する。
- **重要な検証の限界**: 本ADR作成時点で実機確認できたのはBun（1.3.14）
  上での`.node`ロード・`start`/`stop`のライフサイクル・residencyのみ。
  「Node-APIのABI安定性によりNode.js本体でも動作するはず」という期待は
  ABI仕様上妥当だが、**Node.js本体での動作は本環境では未検証**（この
  開発環境ではnodeコマンドの直接実行が許可されていない）。ユーザー環境
  で最初に行うべき確認は`node -e "require('./conduit.node')"`相当の
  ロードテストである。
- 同様に、実際のDiscord Gateway/Cloudflare WorkerへのWebSocket接続が
  native版で成功することも未検証（ネットワーク到達性の確認自体が本環境の
  権限では行えない）。「ビルド・ロード・start/stopが動く」ことと
  「conduitとして実運用できる」ことは別であり、混同しないこと。
- `start`はGOOS=js版から引き継いだ設計（パッケージレベルのグローバルな
  `Manager`）のままなので、1プロセスにつきrelayは1つだけであり、`start`を
  2回呼ぶと1回目のrelayは（明示的な`stop`を挟まなくても）黙って停止・
  置換される。`const relay = await start(config)`というAPIは複数
  インスタンスの共存を連想させる形をしているが、実体は「1プロセス1relay、
  再度の`start`は前のものを置き換える」という単一インスタンス設計である
  点に注意。

### 実装状況（本ADR記載後に追記）

v1の実装は完了し、以下を本開発環境（Bun 1.3.14、linux-x64）で確認済み。

- `cmd/conduit/main_napi.go`（`//go:build napi`）: `napi_register_module_v1`
  で`start`/`stop`/`reload`をflat関数としてexport。`internal/relay`は無改修。
  `cmd/conduit/main.go`のビルドタグは`//go:build !js`から
  `//go:build !js && !napi`に変更（napiタグビルド時の`func main`重複を防ぐ
  ため必須の変更）。既存のタグなしネイティブCLIビルドと
  `GOOS=js GOARCH=wasm`ビルドが退行していないことを確認済み。
- `packages/conduit-relay/src/index.ts`: `start(config): Promise<Relay>`
  （`Relay = {stop, reload}`）に書き換え済み。`.node`のロードは
  `createRequire` + 実行時computed URLで行い、`globalThis`/`global`/`self`
  は未使用（`rg`で確認済み）。
- `packages/conduit-relay/src/cli.ts`: 13行程度まで簡潔化。
  `CONFIG_FILE`を`node:fs`の`readFileSync`で読み、`start`に渡す。
  SIGHUP→reload、SIGTERM/SIGINT→stopは維持。
- `packages/conduit-relay/scripts/build.sh`: `node-api-headers`（npm）から
  includeパスを実行時解決し、`CGO_CFLAGS`経由で`.node`をビルドする
  ステップを追加。GOOS=js版のビルドステップはfallbackとして維持。
- 独立した動作確認（このADRの筆者とは別に、実装したエージェントとは別の
  セッションで実施）: `packages/conduit-relay/dist/index.js`を直接importし、
  `start(configObject)`→ハンドル取得→`reload(configString)`→`stop()`が
  一通り動作。不正な設定に対する`start`のError throwも確認。
  `packages/conduit-relay/dist/cli.js`を`CONFIG_FILE`付きで起動し、
  起動ログの出力とSIGTERMでの正常終了（exit code 0）も確認。
  **いずれも`status: "paused"`のBot設定のみを使っている**（`Manager.Apply`
  は`Status=="active"`のBotに対してのみ`go botRun`する実装のため、
  pausedでは実relayのgoroutine・`net.Dial`・`coder/websocket`のブロッキング
  Readは一度も実行されていない）。residency（バックグラウンド処理が
  JSアイドル中も自律的に進行すること）を実機確認したのは、あくまで
  ping/counterのようなtoy goroutineであり、`internal/relay`本体の
  コードパスではない。**「NAPIラッパー + Managerのライフサイクル
  （active bot 0個）が動く」ことと「実relayのgoroutineがaddon内で
  動き続ける」ことは別の主張であり、後者は本ADR時点で未検証**。
- **wasm版の削除（上記の不整合への対応）**: 実装当初、`scripts/build.sh`は
  `dist/conduit.wasm`と`wasm_exec.js`を生成する一方、`conduit.wasm.d.ts`の
  誤削除を回避するため`tsconfig.json`に`exclude: ["src/load.ts"]`が追加され、
  結果`dist/load.js`が生成されず「ビルドはされるがロードできない」壊れた
  fallbackになっていた。この負債を解消するため、wasm関連（`load.ts`,
  `wasm_exec.js`, `wasm_exec.d.ts`, `conduit.wasm.d.ts`, `build.sh`の
  wasmビルドステップ、`.gitignore`/`biome.json`/`package.json`/READMEの
  wasm参照）を一式削除した。`tsconfig.json`の`exclude`と誤削除していた
  `conduit.wasm.d.ts`は、削除方針確定前に一度整合を取った（excludeを外し
  型定義を復元した）が、最終的にwasm版ごと削除する。

### 起床後に確認すべきこと（優先順）

1. **Node.js本体での動作確認**（本ADRの採用理由そのものが未検証）:
   `node -e "require('./packages/conduit-relay/dist/conduit.node')"`
   相当がエラーなく通るか。次に`packages/conduit-relay/dist/index.js`を
   Node.js本体からimportし、上記と同じ`start`/`reload`/`stop`の
   スモークテストが通るか。
2. **`status: "active"`のBotを最低1つ含む設定でaddonを起動し、実relayの
   goroutineパス（`internal/relay`の`botRun`、`net.Dial`、
   `coder/websocket`のブロッキングRead）がNAPIアドオン内で実際に動作し、
   JSがアイドルの間も自律的に進行し続けることを確認する。** 本ADRで
   実機確認したresidencyはtoy goroutineに対するものであり、実relayの
   コードパスでは未検証（本開発環境ではネットワーク到達性の確認自体が
   行えないため、モックのWebSocketサーバー等を用意する必要がある）。
   その上で実際のDiscord Gateway/Cloudflare WorkerへのWebSocket接続も
   確認する。
3. 作業ツリーに残っている雑多な差分（`.gitignore`, `biome.json`の
   `useGlobalThis`ルール等）が意図通りか確認し、問題なければコミットする
   （本セッションはVCS書き込み操作の権限を持たないため、コミットは
   ユーザー自身が行う必要がある）。
4. 多プラットフォーム配布（`optionalDependencies`によるプラットフォーム別
  `.node`分割）は本ADRのスコープ外のまま。単一VM運用を超えて配布する
  場合は改めて設計が必要。
