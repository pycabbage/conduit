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
- **CLIとライブラリで別々のGo成果物を使う（後日の方針転換、下記参照）**:
  CLI（`npx conduit-relay`、`cli.ts`）は`.node`アドオンもNode-APIレイヤーも
  一切経由しない。既存の`cmd/conduit/main.go`（`//go:build !js && !napi`）を
  そのままビルドしたネイティブ実行可能バイナリを`cli.ts`が
  `child_process.spawn`するだけであり、CONFIG_FILE読込・JSONCパース・
  SIGHUP/SIGTERM/SIGINT処理は全てこのネイティブバイナリが（既存のまま）
  行う。`cli.ts`はstdio/envをinheritしてspawnし、SIGHUPを子プロセスに
  転送し、子の終了コードを伝播するだけの薄いランチャーになる。
  ライブラリ（`index.ts`、`import { conduit } from "conduit-relay"`）は
  `.node`アドオンをNode-APIの生exportのまま（`start`/`stop`/`reload`を
  ラップも変換もせず）提供する。設定は文字列（JSON/JSONC）で
  `conduit.start(configJSON)`のように渡す。
- 配布はプラットフォーム別成果物（`.node`アドオンとネイティブCLIバイナリの
  両方）を`optionalDependencies`で分割する、esbuild/sharp等と同様の定石に
  従う。cgoクロスコンパイルには`zig cc`等の利用を検討する。
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
  `.node`側はflatな関数をexportするだけに留める。文字列引数の受け渡し
  （`napi_get_cb_info`→`napi_get_value_string_utf8`→Go string）は
  実機未検証のため、実装の最初の増分で確認する。
- **多プラットフォーム配布は本ADRのスコープ外とする。** conduitの
  デプロイ先は「Cloudflare外VMで常時稼働」という単一プラットフォーム
  （linux-x64）が前提であり、`optionalDependencies`によるプラットフォーム
  別`.node`分割は将来必要になった時点のfuture workとし、v1では対象
  プラットフォーム1つ向けのビルドのみを行う。

### 方針転換: TS層を最小化する（v1実装後の追記）

上記「v1のスコープ」節に基づく最初の実装では、`.node`のflat exportを
`index.ts`で`start(config): Promise<{stop, reload}>`というハンドル返却API
にラップし、`cli.ts`もそのラッパーを呼ぶ形にしていた（Context節にある
当初のユーザー要求「ハンドルオブジェクトを返す形」に沿ったもの）。

実装後、この設計に対して以下の指摘を受け、方針を転換した。

- CLIが`.node`アドオン（＝ライブラリと同じNode-APIレイヤー）を経由する
  必要はそもそもない。ネイティブCLIバイナリ（`cmd/conduit/main.go`）は
  すでにCONFIG_FILE読込・シグナル処理を自己完結して行っており、
  `cli.ts`はそれを`spawn`するだけで同じことができる。TS側で設定ファイルを
  読み、パースし、`start`を呼ぶ、というロジックは全て不要な重複だった。
- ライブラリ側の`start`/`stop`/`reload`のラップ（`JSON.stringify`、
  エラーの`throw`変換、`{stop, reload}`ハンドルの組み立て）も、
  `.node`の生exportをそのまま使わせれば不要というのがユーザーの判断。
  「JS層は、Go/addonが既にやっている仕事をTSで再実装しない、極限まで薄く
  保つ」という原則を優先し、当初の「ハンドル返却API」要求はこの原則で
  上書きされた（最新の明示指示が優先される）。

結果、CLIとライブラリは別々のGo成果物を使う設計になった（上記Decision節に
反映済み）。`internal/relay`のロジックはどちらの経路でも共通なので、
両者で挙動が分岐する心配はない。

#### `src/index.ts`の実装詳細

`.node`はビルド成果物であり、ソースには存在しない。CommonJSのネイティブ
アドオンをESM（`"type": "module"`）から使うため`createRequire`で橋渡しし、
隣接する成果物（`conduit.node`）は実行時のcomputed URL（`new URL(...,
import.meta.url)`）で解決する。これは著者時点ではアーティファクトが
存在しないため静的importにできないことによる（0008の考え方を踏襲）。
`globalThis`/`global`/`self`は意図的に使用しない。

exportする`conduit`の型（`ConduitAddon`）は`start`/`reload`が成功時に
空文字列、失敗時にエラーメッセージ文字列を返し、`stop`は`undefined`を
返す、という`main_napi.go`の契約をそのまま表す。例外による失敗通知は
行わないため、呼び出し側で戻り値を見て判断する必要がある
（`const err = conduit.start(cfg); if (err) throw new Error(err)`）。

#### `src/cli.ts`の実装詳細（シグナル転送の設計理由）

CLIは`.node`アドオンを一切経由せず、ネイティブCLIバイナリ（`conduit`、
`dist/cli.js`の隣接成果物）を`spawn`するだけの薄いランチャーである。
CONFIG_FILE読込・JSONCパース・SIGHUP（reload）/SIGTERM・SIGINT（graceful
stop）の処理は全てこのネイティブバイナリが自己完結して行う。

SIGHUP/SIGTERM/SIGINTは全て子プロセスへ転送する。理由は配送経路が2通り
あり、どちらでも安全側に倒れるようにするため。

- **single-PID配送**（例: systemdの`KillMode=mixed`はmain pidにのみ
  SIGTERMを送る）の場合、転送しなければネイティブ子プロセスが孤立する。
- **プロセスグループ配送**（対話的なCtrl+C、systemdの既定
  `KillMode=control-group`）の場合、子は直接シグナルを受信するが、
  転送されたコピーは無害なno-opになる（ネイティブバイナリのシグナル
  ハンドラは最初の1つを処理してから終了するため、2つ目は不活性）。

またこれらのハンドラを登録すること自体が、Node.jsのデフォルトの
SIGTERM/SIGINT動作（即座にプロセスを終了させる）を上書きし、子の
graceful shutdown完了前に親（Node.jsプロセス）が終了してしまうのを防ぐ
効果もある。

実機検証（本ADR筆者による）: SIGHUPを送ると子プロセスに転送され
"SIGHUP: reloading config"のログが出ることを確認。続けてSIGTERMを送ると、
親（Node.js）・子（ネイティブバイナリ）とも0.3秒以内に正常終了し、
孤児化は発生しないことを確認した。

#### `cmd/conduit/main_napi.go`の実装詳細

**ビルド**: `-buildmode=c-shared -tags=napi`でビルドする（Node-API公式
ヘッダー`node_api.h`はcgoで直接インクルードする）。

```
CGO_ENABLED=1 \
CGO_CFLAGS="-I/path/to/node-api-headers/include" \
go build -tags=napi -buildmode=c-shared -o conduit.node ./cmd/conduit
```

`node_api.h`のインクルードパスは`CGO_CFLAGS`で外部から渡す前提であり、
ソースやコミット済みのビルドスクリプトに環境固有パス（探索時に使った
`/nix/store/.../nodejs-slim-24.16.0/include/node`等）をハードコードしては
ならない（`node-api-headers` npmパッケージやNode.jsインストールの
includeディレクトリを指すこと）。

**`relay.Manager`のラップ**: `main.go`（ネイティブCLI）が使う
`relay.Manager`（`NewManager`/`Apply`/`StopAll`）をそのまま使い、
エントリポイントだけを差し替える。共有ライブラリは実OSスレッド上で動くため、
`net.Dial`・`coder/websocket`のブロッキングRead・goroutineスケジューリングは
（wasmビルドと異なり）無改修で動作する。

**cgo previewの構造**: `//export`で公開するGo関数（`conduitStart`,
`conduitStop`, `conduitReload`）は、cgoのpreamble内では定義せず前方宣言のみ
にする（`//export`を使うファイルのpreambleは宣言のみである必要がある
制約による）。`conduit_define`（`napi_create_function`+
`napi_set_named_property`で1つのJS関数を`exports`に登録する）と
`conduit_define_all`（start/stop/reloadを登録する）はpreamble内の`static`
なCヘルパー関数として定義する（`static`は内部リンケージなので、cgoの
前方宣言と衝突しない）。`napi_create_function`はC関数ポインタを要求し、
Goの`//export`シンボルのアドレスをCから取る必要があるため、この
ヘルパーが必要になる。

**ヘルパー関数**:
- `jsUndefined`: JSの`undefined`を返す（voidを返すコールバックの戻り値に
  使う）。呼び出しが失敗したら`NULL`にフォールバックする（Node側では
  `NULL`も`undefined`として扱われる）。
- `jsString`: GoのstringからJS文字列を作る。
- `firstStringArg`: コールバックの第1引数をUTF-8文字列として読む。
  Node-APIの標準的な2回呼び出しパターン（`napi_get_value_string_utf8`を
  まず`buf=NULL, bufsize=0`で呼んで必要バイト数を取得し、その後バッファを
  確保して再度呼ぶ）に従う。
- `applyStart`: `configJSON`をパースし、既存のmanagerがあれば`StopAll`して
  から新しいmanagerに置き換えて`Apply`する（＝1プロセス1relay、2回目の
  `start`は1回目を置き換える設計。前述のとおり意図的）。
- `applyReload`: 既存のmanagerに新しい設定を`Apply`する。managerが
  存在しない場合はエラーを返す。
- `conduitStart`/`conduitReload`が返す空文字列は成功を意味する（ADR記載の
  「エラーは`start`の同期的な戻り値で通知する」設計の実体）。
- `main()`は`package main`に必要なだけの空実装。`-buildmode=c-shared`では
  実行されない（アドオンはexportされたN-API関数経由でのみ駆動される）が、
  これがないと（buildmode指定なしの）`go build -tags=napi`単体が失敗する。

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
- `packages/conduit-relay/src/index.ts`: 上記「方針転換」節の通り、
  `.node`アドオンの生exportのみ（`export const conduit = require(...)`）。
  ラッパー・ハンドルオブジェクト・エラーthrow変換は持たない。
  `.node`のロードは`createRequire` + 実行時computed URLで行い、
  `globalThis`/`global`/`self`は未使用（`rg`で確認済み）。
- `packages/conduit-relay/src/cli.ts`: 薄いspawnランチャー（10行程度）。
  ネイティブCLIバイナリ（`dist/conduit`、`.node`アドオンとは別成果物）を
  `child_process.spawn`し、`stdio`/`env`をinherit、SIGHUP/SIGTERM/SIGINTを
  子プロセスへ転送、子のexit codeを伝播する。設定ファイルの読込・パース・
  `start`相当の呼び出しはTS側に一切ない（ネイティブCLIバイナリが
  `cmd/conduit/main.go`のまま自己完結して行う）。
- `packages/conduit-relay/scripts/build.sh`: `dist/conduit`（タグなし
  ネイティブCLIバイナリ、`CGO_ENABLED=0`）と`dist/conduit.node`（NAPI
  アドオン、`node-api-headers`からincludeパスを実行時解決）の両方を
  ビルドする。wasm関連ステップは削除済み（後述）。
- 独立した動作確認（このADRの筆者とは別に、実装したエージェントとは別の
  セッションで実施）:
  - ライブラリ: `packages/conduit-relay/dist/index.js`を直接importし、
    `conduit.start(configJSON)`→空文字列（成功）、`conduit.reload(...)`→
    空文字列、`conduit.stop()`→戻り値なし、`conduit.start("not json")`→
    Goのエラー文字列がそのまま返る（throwしない、生の戻り値）ことを確認。
  - CLI: `dist/cli.js`を`CONFIG_FILE`付きで起動し、`bun`（親）と
    `dist/conduit`（子）の2階層プロセスツリーが立つことを確認。親プロセスに
    **SIGHUPを送ると子に転送され"SIGHUP: reloading config"のログが出る
    ことを確認。続けてSIGTERMを送ると、子プロセスも含めて0.3秒以内に
    両方とも正常終了することを確認**（子プロセスの孤児化なし）。
  - **いずれも`status: "paused"`のBot設定のみを使っている**（`Manager.Apply`
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
- **`cmd/conduit/main_js.go`の削除**: TS側のwasmローダーを削除した後も
  Go側の`main_js.go`（`//go:build js`）だけが残っていた。`packages/
  conduit-relay/scripts/build.sh`からGOOS=jsビルドの呼び出しは既に消えて
  おり、他のどこからも参照されない（`main_napi.go`のコメントに
  ファイル名への言及が1箇所あっただけ）孤立したファイルになっていたため
  削除した。ADR 0006/0007（Superseded）に設計判断の記録が残るため、
  将来wasmビルドが必要になれば復元できる。削除後、タグなしネイティブCLI
  ビルド（`go build ./cmd/conduit`）と`napi`タグ+`c-shared`ビルドの両方が
  引き続き成功することを確認済み。

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
