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

### 方針転換2: NAPI shimとCLIをpure Cへ分離する（1プロセス1 Goランタイム）

上記「別々のGo成果物」設計（`main.go`をタグなしでビルドしたネイティブCLI
バイナリ + `main_napi.go`を`-buildmode=c-shared -tags=napi`でビルドした
`.node`アドオンの2成果物、いずれも`internal/relay`を直接importしてGoの
コードとして完結）は、実装後にユーザーが`./dist/conduit.linux-x64.node`を
直接実行したところsegfaultすることが発覚し、破綻していると判明した。

**原因（実機検証済み）**: `-buildmode=c-shared`が生成するELFには
エントリポイント/`PT_INTERP`が無く、カーネルローダーがそれを直接
execしようとするとクラッシュする。逆に`-buildmode=pie`にすればエントリ
ポイント/`PT_INTERP`が付き直接実行可能になるが、今度はBunの`require()`が
`dlopen`で失敗する（`ERR_DLOPEN_FAILED: cannot dynamically load
position-independent executable`）。glibcの動的ローダーはPIE実行体の
dlopenを拒否するため。つまり**「直接execできる」ことと「dlopenできる」
ことは、Linux上の1つのELFファイルでは両立しない**。「CLIとライブラリで
別々のGo成果物を使う」という上記の方針転換は、この問題を「1つのGo
成果物をrequireとexecの両方に使う」という壊れた構成のまま放置していた
ことになる。

**新しい設計**: Go成果物を1つだけに絞り、Node-API依存のNode-APIグルー層
とCLIのシグナル処理層をpure C（Goランタイムを持たない）に切り出す。

- **`cmd/conduit/main_core.go`（唯一のGo成果物）**: `//go:build core`。
  `node_api.h`への依存を一切持たない、汎用のフラットなC ABI
  （`ConduitStart`/`ConduitStop`/`ConduitReload`、いずれも`*C.char`の
  プレーンなC文字列で入出力する）を`//export`する。`-buildmode=c-shared`
  で`libconduitcore.so`（+副産物の`libconduitcore.h`）としてビルドする。
  `internal/relay`がこのnpmパッケージ向けに機械語へコンパイルされる場所は
  ここだけになる。
- **`packages/conduit-relay/native/napi_shim.c`（pure C、Goなし）**:
  `node_api.h`を直接includeし、`libconduitcore.so`の上記C ABIを呼ぶだけの
  Node-APIグルー。`gcc -shared -fPIC`でビルドし、これが`require()`される
  唯一のファイル（`conduit.${platform}-${arch}.node`）になる。直接execする
  ことは想定しない。
- **`packages/conduit-relay/native/cli.c`（pure C、Goなし）**: 旧
  `cmd/conduit/main.go`のCONFIG_FILE読込・SIGHUP/SIGTERM/SIGINT処理を
  Cで再現し、`libconduitcore.so`の同じC ABIを呼ぶ。`gcc`で通常の実行可能
  ファイル（`conduit.${platform}-${arch}`、拡張子なし）としてビルドする。
  これが`cli.ts`が`spawn`する唯一のファイルであり、require()されることは
  想定しない。
- 両C成果物は`-Wl,-rpath,$ORIGIN`で`libconduitcore.so`を動的リンクし、
  3ファイルとも`dist/`に同居することで実行時に解決できる。

**pure Cでなければならない理由（Goにしてはいけない理由）**: `-buildmode=
c-shared`でビルドしたGo成果物は、それをロードしたプロセスに専用の
Goランタイム（goroutineスケジューラ・GC・OSスレッド）を1つ埋め込む。
napi shimやCLI実行体も**Go**でビルドして`libconduitcore.so`にリンクして
しまうと、1プロセスに独立した2つのGoランタイムが共存する構成になる。
これは未検証かつ不必要な構成である。そもそもこのADR全体の
native方式採用の前提（「バックグラウンドgoroutineがホストのアイドル中も
自律的に進行し続ける」= residency）は、**プロセスに埋め込まれたGo
ランタイムが1つだけ**という条件下で実機検証したものであり、この前提を
崩さないために、shim/CLI側は常にpure Cで書く。

**本redesignのための実機検証（toy prototype、上記wasip1/native比較と
同様の位置付けで記録）**:

- (a) バックグラウンドgoroutineでカウンタを進めるだけの最小Go
  `c-shared`ライブラリを、`gcc`+`-Wl,-rpath,'$ORIGIN'`でpure Cの実行可能
  ファイルにリンクした。直接実行してsegfaultしないこと（正しい
  エントリポイント/`PT_INTERP`を持つこと）、およびホストアイドル1秒間に
  カウンタが正しく進行すること（residency維持）を確認した。
- (b) 上記実行可能ファイルにSIGHUPハンドラを追加し、ハンドラから
  Goがexportした関数を正しく呼び出せることを確認した。
- (c) 同じcoreライブラリに対するpure C・GoゼロのNode-API shim
  （`node_api.h`のみに依存）をBun 1.3.13から`require()`し、成功すること、
  かつJS側の500msアイドル中もカウンタが正しく進行すること（residency
  維持）を確認した。
- 上記いずれもクラッシュなし・residency維持・シグナルからGo関数呼び出しへ
  の到達を確認済み。

**サイズ測定と3成果物設計を選んだ理由**: Node-API依存のみでrelayロジックを
含まない最小Go `c-shared`ビルドはstrip後約1.38MB。これに対し、
`internal/relay` + `coder/websocket`とそのTLS/HTTP/JSON依存閉包を静的
リンクした（旧設計の）フルアドオンはstrip後約7.1MB。旧「別々のGo成果物」
設計では、この重量級の部分（約5.7MB）がCLIバイナリと`.node`アドオンの
両方に重複して埋め込まれていた。新設計はGo成果物を1つに絞ることで、この
重複をほぼ解消する（旧「2 Go成果物」設計に対して概ね50%のサイズ削減）。

**C文字列の所有権契約（生C ABI）**: `ConduitStart`/`ConduitReload`は
成功時に`NULL`を返す（空文字列を`C.CString("")`で確保するアロケーション
コストを成功パスで払わないため、`NULL`を成功センチネルとする）。失敗時は
`C.CString(err.Error())`でmallocされた文字列を返し、**呼び出し側
（`napi_shim.c`/`cli.c`）がfreeする責務を持つ**。これはJSから見える
`conduit.start()`の契約（成功時`""`、失敗時エラー文字列、README記載の
「成功時は空文字列」）とは異なるセンチネルであり、この変換は`napi_shim.c`
の`conduit_result`が担う（`NULL`→JSの`""`、非`NULL`→JS文字列化して
`free()`）。生C ABIと、JSに見せる契約を意図的に分離した設計である。

**`libconduitcore.h`の位置付け（旧ADRの判断を上書き）**: 旧設計では
`.h`（`go build -buildmode=c-shared`の副産物）を「Node-APIモジュールは
`require()`でロードするだけなので不要」として`dist/`から削除していた
（この判断自体は旧設計では正しかった。旧`main_napi.go`はNode-APIの
シンボルを直接exportしており、Cヘッダー経由で他のCコードから呼ばれる
ことは無かったため）。新設計では`napi_shim.c`・`cli.c`の両方が
`libconduitcore.h`（`ConduitStart`/`ConduitStop`/`ConduitReload`の
シグネチャ）を`#include`してビルドされるため、**`libconduitcore.h`は
正真正銘のビルド時依存になった**。ただしビルド後は`dist/`から削除し、
npmパッケージには同梱しない（実行時に必要になることはない。3成果物は
すべて`libconduitcore.so`にリンク済みで、ヘッダーはコンパイル時にしか
要らない）。

#### `src/index.ts`の実装詳細

`.node`はビルド成果物であり、ソースには存在しない。CommonJSのネイティブ
アドオンをESM（`"type": "module"`）から使うため`createRequire`で橋渡しし、
隣接する成果物（`conduit.node`）は実行時のcomputed URL（`new URL(...,
import.meta.url)`）で解決する。これは著者時点ではアーティファクトが
存在しないため静的importにできないことによる（0008の考え方を踏襲）。
`globalThis`/`global`/`self`は意図的に使用しない。

exportする`conduit`の型（`ConduitAddon`）は`start`/`reload`が成功時に
空文字列、失敗時にエラーメッセージ文字列を返し、`stop`は`undefined`を
返す、という`native/napi_shim.c`の契約をそのまま表す（`libconduitcore.so`
の生C ABIは`NULL`/malloc済みエラー文字列というセンチネルだが、
`napi_shim.c`がJS向けに`""`/文字列へ変換する。詳細は上記「方針転換2」の
C文字列所有権契約を参照）。例外による失敗通知は行わないため、呼び出し側で
戻り値を見て判断する必要がある
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

上記の実機検証は「方針転換2」で3成果物split前、`spawn`先が
`cmd/conduit/main.go`をタグなしでビルドした実行体だった時点のものである。
`spawn`先を差し替える`cli.ts`側の設計（シグナル転送の理由・二重配送への
対応）自体は変わらず有効だが、シグナルを受け取る側の実装は
`packages/conduit-relay/native/cli.c`（pure C、下記）に置き換わっており、
SIGHUP→SIGHUP→SIGTERMのような複数シグナルの連続配送に対する再検証が
必要（「起床後に確認すべきこと」参照）。

#### `cmd/conduit/main_core.go`・`native/napi_shim.c`・`native/cli.c`の実装詳細

**ビルド**: `main_core.go`は`-buildmode=c-shared -tags=core`でビルドする。
`node_api.h`への依存が無いため、`CGO_CFLAGS`でNode-APIヘッダーの
includeパスを渡す必要は無い（`CGO_ENABLED=1`のみで十分）。

```
CGO_ENABLED=1 \
go build -tags=core -buildmode=c-shared \
  -o libconduitcore.so ./cmd/conduit
```

`native/napi_shim.c`・`native/cli.c`は`gcc`で直接ビルドする（`go build`は
使わない）。いずれも`libconduitcore.h`（`libconduitcore.so`ビルドの副産物）
を`#include`し、`-L. -lconduitcore -Wl,-rpath,$ORIGIN`で
`libconduitcore.so`を動的リンクする。`napi_shim.c`は
`gcc -shared -fPIC`（Node-APIヘッダーのincludeパスを`-I`で追加）、
`cli.c`は通常の実行可能ファイルとしてビルドする（特別な`-buildmode`
相当のフラグは不要）。3ファイルとも`dist/`に同居させることで、
実行時に`$ORIGIN`（=自分自身のディレクトリ）から`libconduitcore.so`を
解決できる。

`node_api.h`のインクルードパスは`CGO_CFLAGS`（`napi_shim.c`をビルドする
`gcc`呼び出しには`-I`）で外部から渡す前提であり、ソースやコミット済みの
ビルドスクリプトに環境固有パス（探索時に使った
`/nix/store/.../nodejs-slim-24.16.0/include/node`等）をハードコードしては
ならない（`node-api-headers` npmパッケージやNode.jsインストールの
includeディレクトリを指すこと）。

**`relay.Manager`のラップ（`main_core.go`）**: `main.go`（ネイティブCLI、
`//go:build !core`）が使う`relay.Manager`（`NewManager`/`Apply`/
`StopAll`）をそのまま使い、エントリポイントだけを差し替える。共有
ライブラリは実OSスレッド上で動くため、`net.Dial`・`coder/websocket`の
ブロッキングRead・goroutineスケジューリングは（wasmビルドと異なり）
無改修で動作する。`main_core.go`は`node_api.h`も`napi_env`/`napi_value`も
一切参照しない、生の`*C.char`だけを扱う汎用C ABIである。旧
`main_napi.go`にあったcgo preamble内の`static` Cヘルパー（後述）は
Node-API固有のものだったため不要になり、`main_core.go`のcgo preambleは
空（`import "C"`の直前にpreambleコメント自体が無い）。

**`ConduitStart`/`ConduitReload`が返すポインタの契約**: 成功時は`NULL`
（呼び出し側でのfree不要）、失敗時は`C.CString(err.Error())`（呼び出し側
＝`napi_shim.c`/`cli.c`が`free()`する責務を持つ）。旧`main_napi.go`の
`conduitStart`/`conduitReload`が返していた「成功時は空文字列」は
JS向けの契約であり、`main_core.go`の生C ABIでは`NULL`センチネルに
置き換わった（詳細は上記「方針転換2」のC文字列所有権契約を参照）。

**`native/napi_shim.c`の構造**: `node_api.h`のみをinclude
する（`libconduitcore.h`は`ConduitStart`等のシグネチャを得るため
別途include）。旧`main_napi.go`のcgo preambleにあった`static`な
Cヘルパー（`conduit_define`/`conduit_define_all`、`napi_create_function`
がC関数ポインタを要求するためGoの`//export`シンボルをcgo preamble内の
Cヘルパー経由で登録する必要があった）は、pure Cになったことで
その制約自体が消え、`napi_shim.c`内で直接`napi_create_function`を
呼ぶだけで済む（`conduit_define`はコード重複を避けるための単なる
ローカルヘルパーとして残すが、cgoの前方宣言制約に起因するものではない）。
モジュール初期化は`node_api.h`が提供する`NAPI_MODULE_INIT()`マクロ
（`napi_register_module_v1`相当を展開する）を使う。

**`native/napi_shim.c`のヘルパー関数**:
- `conduit_string_arg`: コールバックの第1引数をUTF-8文字列として読む。
  Node-APIの標準的な2回呼び出しパターン（`napi_get_value_string_utf8`を
  まず`buf=NULL, bufsize=0`で呼んで必要バイト数を取得し、その後バッファを
  確保して再度呼ぶ）に従う。旧`main_napi.go`の`firstStringArg`（Go実装）
  をCへ直訳したもの。
- `conduit_result`: `libconduitcore.so`が返す生ポインタ（`NULL`/malloc済み
  エラー文字列）をJS文字列（成功時`""`、失敗時エラー文字列）へ変換する。
  非`NULL`の場合はJS文字列を作った後に`free()`する（Go側の`C.CString`が
  確保した領域を、この関数が解放する）。
- `Start`/`Reload`: `conduit_string_arg`で引数を読み、`ConduitStart`/
  `ConduitReload`を呼び、`conduit_result`で変換して返す。読み取った
  引数バッファは呼び出し後に`free()`する。
- `Stop`: `ConduitStop()`を呼び、`napi_get_undefined`を返す。

**`native/cli.c`の構造**: 旧`main.go`のCONFIG_FILE読込・
SIGHUP/SIGTERM/SIGINT処理をCで再現する。`readConfigFile`
（`fopen`/`fseek`/`ftell`/`fread`/`fclose`でファイル全体をヒープバッファへ
読む）を起動時と各SIGHUP時の両方で使う。シグナル配送は
`signal()`/シグナルハンドラ内での処理ではなく、`sigprocmask(SIG_BLOCK,
...)`で事前にSIGHUP/SIGTERM/SIGINTをブロックし、メインループで
`sigwait()`により同期的に1つずつ受け取る設計にした（非同期シグナル
ハンドラ内でGoランタイムへの呼び出しや`exit()`を行う
async-signal-safety上のリスクを避けるため。ハンドラ内で処理する
古典的な自前ポーリング/self-pipeパターンより単純かつ「取り逃し」の
競合が原理的に発生しない）。SIGHUPで`reload`（未`start`ならまず
`ConduitStart`、既に`started`なら`ConduitReload`）、SIGTERM/SIGINTで
`ConduitStop()`を呼んで`return 0`する。`started`フラグは
`ConduitStart`が`NULL`（成功）を返した時にのみ立てる
（設定ファイルのパース失敗等で`ConduitStart`がエラーを返した場合は
managerが作られていないため、次のSIGHUPは`ConduitReload`ではなく
再度`ConduitStart`を呼ぶ必要がある）。

`main_core.go`の`func main() {}`は`package main`に必要なだけの空実装で
（`-buildmode=c-shared`では実行されず、`libconduitcore.so`はexportされた
C ABI関数経由でのみ駆動される）、これが無いと`go build -tags=core`単体が
失敗する。

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

### 実装状況（本ADR記載後に追記、v1時点の記録。3成果物split前）

**この節はv1（`main.go`実行体 + `main_napi.go`の`.node`という「別々のGo
成果物2つ」設計）時点の記録であり、後述「方針転換2」で説明した
segfaultの発覚によりこの設計は破棄された。歴史的経緯として残す
（「main_napi.go」への言及はすべて削除済みファイルを指す）。**

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

### 実装状況2（「方針転換2」3成果物split後に追記）

上記v1設計のsegfault発覚を受け、「方針転換2」節の設計へ移行した。
本開発環境（linux-x64、go 1.26.4、gcc 15.2.0）で以下を確認済み。

- `cmd/conduit/main.go`: ビルドタグを`//go:build !js && !napi`から
  `//go:build !core`へ変更（`main_js.go`は既に削除済みのため`!js`は不要、
  `main_napi.go`削除・`main_core.go`追加に伴い`!napi`を`!core`へ置換）。
- `cmd/conduit/main_napi.go`を削除。
- `cmd/conduit/main_core.go`（`//go:build core`）を新規作成。旧
  `main_napi.go`の`parseConfigs`/`applyStart`/`applyReload`/
  `mgrMu`/`mgr`をそのまま移植し、`ConduitStart`/`ConduitStop`/
  `ConduitReload`という生C ABIをexportする。
- `packages/conduit-relay/native/napi_shim.c`・`native/cli.c`を新規作成
  （pure C、Goゼロ）。
- `packages/conduit-relay/scripts/build.ts`を全面書き換え: Go core
  ライブラリのビルド→（tsc、napi shimのgccビルド、cliのgccビルドを並列
  実行）→`libconduitcore.h`の削除、という順で実行する。旧
  `scripts/build.sh`は削除し、`package.json`の`build`スクリプトを
  `bun run scripts/build.ts`に変更。
- `packages/conduit-relay/src/loader.ts`の`getBindingPath()`を、
  `.node`と同じパスを返す不具合（今回のsegfaultの直接原因）から
  拡張子なしの実行体パス（`conduit.${platform}-${arch}`、
  optionalDependencyフォールバックも同様）を返すよう修正。`getBinding()`
  （`.node`用）は無変更。
- `packages/conduit-relay/src/cli.ts`を、直接`spawn(join(dirname,
  "./conduit"))`していた実装から`spawn(await getBindingPath(), ...)`に
  変更（`loader.ts`経由でプラットフォーム別実行体パスを解決するよう
  統一）。`src/index.ts`は無変更。
- ビルド確認: `bun run build`が exit 0 で完了し、`dist/`に
  `libconduitcore.so`・`conduit.linux-x64.node`・`conduit.linux-x64`
  （拡張子なし）の3ファイルが生成されることを確認。`readelf -d`で
  両C成果物が`NEEDED libconduitcore.so`と`RUNPATH`の先頭に`$ORIGIN`
  （リテラル、シェルクオート無し）を持つことを確認（`-Wl,-rpath,$ORIGIN`
  が正しく効いている証跡）。
- **今回のバグの再現確認**: `CONFIG_FILE=/nonexistent
  ./dist/conduit.linux-x64`を直接実行し、以前のようにsegfaultせず
  「config read: failed to read /nonexistent」を標準エラーに出して
  正常に起動（シグナル待ち）することを確認した（ごく簡易な確認であり、
  下記オープン項目のSIGHUP/SIGTERM連続配送の確認はまだ行っていない）。
- **未検証（次フェーズ）**: `require()`による`.node`ロード、
  `start`/`stop`/`reload`のライフサイクル、実relayのgoroutineパスの
  residency、実際のDiscord Gateway/Cloudflare Workerへの接続——これらは
  いずれもv1時点の未検証事項と同様に、3成果物split後の構成でも
  未検証のまま持ち越し。

### 起床後に確認すべきこと（優先順）

1. **Node.js本体での動作確認**（本ADRの採用理由そのものが未検証）:
   `node -e "require('./packages/conduit-relay/dist/conduit.node')"`
   相当がエラーなく通るか。次に`packages/conduit-relay/dist/index.js`を
   Node.js本体からimportし、上記と同じ`start`/`reload`/`stop`の
   スモークテストが通るか。
2. **`native/cli.c`のシグナル処理が現実的な連続配送に耐えるかの再検証**:
   本ADRのtoy prototype（「方針転換2」節の(b)）ではSIGHUPを1回配送する
   ケースしか検証していない。SIGHUP→SIGHUP→SIGTERMのように複数の
   シグナルを連続して送った場合に、reloadが2回とも正しく発火し
   （1回目・2回目とも`started`が正しく更新され、`ConduitReload`が
   呼ばれる）、stopが1回だけ発火し、クラッシュや同じCエラー文字列ポインタ
   の二重`free()`が発生しないことを実機で確認する。
3. **`status: "active"`のBotを最低1つ含む設定でaddonを起動し、実relayの
   goroutineパス（`internal/relay`の`botRun`、`net.Dial`、
   `coder/websocket`のブロッキングRead）がNAPIアドオン内で実際に動作し、
   JSがアイドルの間も自律的に進行し続けることを確認する。** 本ADRで
   実機確認したresidencyはtoy goroutineに対するものであり、実relayの
   コードパスでは未検証（本開発環境ではネットワーク到達性の確認自体が
   行えないため、モックのWebSocketサーバー等を用意する必要がある）。
   その上で実際のDiscord Gateway/Cloudflare WorkerへのWebSocket接続も
   確認する。
4. 作業ツリーに残っている雑多な差分（`.gitignore`, `biome.json`の
   `useGlobalThis`ルール等）が意図通りか確認し、問題なければコミットする
   （本セッションはVCS書き込み操作の権限を持たないため、コミットは
   ユーザー自身が行う必要がある）。
5. 多プラットフォーム配布（`optionalDependencies`によるプラットフォーム別
  `.node`分割）は本ADRのスコープ外のまま。単一VM運用を超えて配布する
  場合は改めて設計が必要。
