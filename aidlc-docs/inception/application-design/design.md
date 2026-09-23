# moviedl 設計書

## アーキテクチャ

Wails v2 を使用する。Go バイナリが WebView ウィンドウを内包し、フロントエンド（HTML/CSS/JS）と Go バックエンドが IPC で通信する。

- フロントエンド: 単一 HTML ファイル（フレームワークなし）
- バックエンド: Go
- IPC: `window.go.main.App.*`（JS → Go）/ `window.runtime.EventsOn`（Go → JS）
- クリップボード: `window.runtime.ClipboardSetText(text)` を使う（Wails ランタイム）。WebView の `navigator.clipboard` はセキュアコンテキスト要件等で不安定なため使わない。URL コピーはこれで実装する。

### バージョン情報の埋め込み

- `main.go` にパッケージ変数 `version`（既定 `"dev"`）と `buildDate`（既定空）を置き、ビルド時に `-ldflags "-X main.version=... -X main.buildDate=..."` で注入する。
- 表示文字列は純粋関数 `formatVersion(version, buildDate)` で組み立てる（`dev` のときだけビルド日を併記）。`AppVersion()` がそれを返し、フロントエンドがタイトル横に表示する。
- リリース（`release.yml`）は git タグ（`github.ref_name`）を `version` に注入する。タグ名はシェル展開の安全のため `env:` 経由で渡す。
- ローカル `make build` は `git describe --tags --always --dirty` と日付を注入する。

---

## 動画ダウンロード

### yt-dlp

動画の取得に yt-dlp を使用する。

- 実行ファイルは `os.UserConfigDir()/moviedl/` に配置する
- 未インストールの場合はアプリ内からダウンロードできる

#### 引数インジェクション対策（`--` 終端は必須）

**やってはいけないこと:** yt-dlp に URL を渡すとき、オプション終端 `--` を付けずに位置引数として渡す。

**なぜか:** yt-dlp は `-` で始まる引数をオプションとして解釈する。URL 文字列が `--exec=...` / `--config-location=...` / `--batch-file=...` などに化けると、yt-dlp 経由で任意コマンド実行・任意ファイル読み書きが成立し得る（RCE 相当）。
さらにこれは自己入力に限らない。`FetchPlaylist`（`--flat-playlist`）が取得するプレイリストエントリの `webpage_url` / `url` は **リモート（動画サイト側）が制御できる値**であり、悪意あるサイトがエントリ URL に `--exec=...` を仕込めば、ユーザーが「キューに追加」した時点で `runDownload` 経由でインジェクションが成立する。

**正しい代替手段:**

- yt-dlp を起動する **すべての** `exec.Command`（`FetchPlaylist` / STEP2a のタイトル取得 / 本ダウンロード）で、URL の直前に `"--"` を挿入し、以降を位置引数に固定する。

```go
exec.Command(ytdlp, "--flat-playlist", "--dump-json", "--no-warnings", "--", rawURL)
exec.Command(ytdlp, "--skip-download", "--dump-json", "--no-playlist", "--", item.URL)
args = append(args, "--", item.URL) // 本ダウンロード
```

- 加えて、登録時に `http://` / `https://` で始まらない URL を弾く（`isValidURL`）。`--` 終端と多層で守る。`AddToQueue` と `FetchPlaylist` の入口で検証する。

`applyOSProcAttr` と同様、**1 箇所でも `--` が漏れるとその経路だけ穴が空く**。yt-dlp を呼ぶ箇所を追加するときは必ず `--` 終端と URL 検証を通すこと。

#### インストール時の完全性検証

`InstallYtDlp` は GitHub Releases から HTTPS で取得する（TLS 検証はデフォルト有効）。取得したバイナリは `0o755` で保存され、その後実行される。

- **SHA256 を照合してから保存・実行する。** 同じリリースの `SHA2-256SUMS` を取得し、対象アセット名の行のダイジェストと、ダウンロードしたバイト列の SHA256 を突き合わせる。不一致なら保存せずエラーにする。
- `http.Get`（タイムアウトなし）ではなく、タイムアウト付き `http.Client` を使う（ハング防止）。
- 検証は「まずメモリ（またはテンポラリ）に受けて SHA256 を確認 → 一致したら最終パスへ書き込む」順序にする。検証前の実体を最終パスに置かない。

**残存リスク（完全性 ≠ 真正性）:** SHA は**バイナリと同じ GitHub リリース**から取る（`SHA2-256SUMS` /
`checksums.sha256`）。これは**転送破損**を検出するが、**リリース自体が侵害された場合は検出できない**
（チェックサムも同時に差し替えられるため）。また `releases/latest` 参照のため**バージョン固定はなく**、
配置後のバイナリ（ユーザー書き込み可能な設定フォルダ内）は**起動時に再検証されない**ため、ローカル
マルウェアによる差し替えは検出できない。これはデスクトップアプリとして許容するトレードオフだが、
強化するなら（a）バージョンピン + ピン時点ダイジェストの同梱、（b）起動時の実行ファイル再検証、を
検討する。（2026-06-10 セキュリティ監査 T3）

#### yt-dlp の更新（UpdateYtDlp）

要件は [requirements.md「yt-dlp の更新」](../requirements/requirements.md) を参照。手動更新のみ（自動チェックなし）。

**手順の順序を守ること（順序自体が要件）:**

```
1. 影響件数を数える（生存プロセスがあれば確認を求める。無ければ確認なしで続行）
2. 新版をダウンロードして SHA256 照合    ← ここまで既存環境への影響ゼロ
3. 生存している yt-dlp プロセスを全停止
4. 検証済みの実体を os.Rename で原子的に配置
5. 停止したアイテムを "queued" に戻す
```

**やってはいけないこと:** 先に停止してからダウンロードを始める。

**なぜか:** ネットワーク失敗・チェックサム不一致で更新が中断したとき、**ユーザーのダウンロード進捗だけが失われて何も得られない**。停止は不可逆（進捗は復元できない、下記参照）なので、不可逆な操作は「もう失敗しない」ところまで来てから行う。

**正しい代替手段:** 上記の順序。ステップ 2 までは一時ファイルへの書き込みしかせず、失敗しても既存の yt-dlp とダウンロードは無傷のまま。既存の `InstallYtDlp` が既にこの「一時ファイルへ受けて検証 → 一致したら rename」構造なので、停止をその間に挟む形にする。

##### 停止対象は「実行中」だけでは足りない

**やってはいけないこと:** `Status == "downloading"` のアイテムだけ止めて更新する。

**なぜか:** 取りこぼす経路が **4 つ**ある。

| 取りこぼす経路 | 理由 |
|---|---|
| 一時停止中のアイテム | 一時停止は `SIGSTOP` / `NtSuspendProcess` による**サスペンド**であり、プロセスは生きている。Windows では実行ファイルを掴んだままなので `os.Rename` での上書きが共有違反で失敗する |
| `FetchPlaylist` の情報取得 | `cmd` がローカル変数で、Go 側のどこにも記録されていない。「取得中」表示もフロントエンドのローカル状態（`fetching`）でしかない |
| `runDownload` STEP2a のタイトル取得 | `item.cmd` にセットされる前の別プロセスなので、アイテム経由では停止できない |
| **yt-dlp が起動する ffmpeg 孫プロセス** | **yt-dlp を停止しても ffmpeg は生き残る。**（2026-09-21 追記。下記「停止は孫プロセスまで及ばせる」を参照） |

**正しい代替手段:** `App` に **yt-dlp プロセスの登録簿**を持ち、yt-dlp を起こす**すべての** `exec.Command` が登録・解除を通る構造にする。`applyOSProcAttr` と `--` 終端と同じ「1 箇所でも漏れたらその経路だけ穴が空く」性質のルールである。登録簿があれば「今 N 件生きている」を正確に数えられ、確認ダイアログの件数も正しくなる。

**ただし登録簿だけでは足りない。** 登録簿が数えるのは yt-dlp プロセスであって、yt-dlp が産む孫プロセスではない。
停止はプロセス**ツリー**単位で行う必要がある（次節）。

一時停止中のプロセスは **resume してから Kill する**（サスペンド中は SIGKILL を受け取れない）。`CancelDownload` が [queue.go](../../../queue.go) で既に同じ順序を実装しているので、それに倣う。

##### 停止したダウンロードは 0% からやり直しになる

`workDir` は実行ごとに `os.MkdirTemp` で新規作成され、`runDownload` の defer で丸ごと削除される。部分ファイルを次回実行へ引き継ぐ仕組みは**ない**ため、停止したダウンロードは再開ではなく再実行になる。これは既知の制約であり、更新の確認ダイアログでユーザーに明示する（黙って捨てない）。

##### 更新後の再開

停止したアイテムは `"queued"` に戻す。同時ダウンロード数が 1 以上なら自動補充ルールで先頭から再開される。0（登録のみモード）なら待ちキューに留まる。**更新の完了後に** `"queued"` へ戻すこと（先に戻すと、置き換え前の古いバイナリで scheduler が再起動してしまう）。

##### `"queued"` を完了時削除の対象にしてはいけない

**やってはいけないこと:** `runDownload` の末尾で「`"error"` 以外はリストから削除」と判定する。

**なぜか:** 更新のために Kill されたアイテムは `cmd.Wait()` のエラー経路を通って `"queued"` になり、そのまま `runDownload` の末尾に到達する。`"error"` 以外を削除する判定だと、**再開できるはずのアイテムがリストから消える**。

**正しい代替手段:** 判定を `shouldRemoveWhenDone(status)` に切り出し、`"error"`（リトライ・URL コピーのため）と `"queued"`（更新のため停止して再キューしたもの）の両方を残す。新しい「残すべき状態」が増えたらこの関数だけを直す。

あわせて、Kill された yt-dlp の `Wait` エラーを `"error"` にしないため `DownloadItem.stopFlag`（atomic）を使う。`cancelFlag` と同じパターンで、`isStoppedForUpdate()` が真なら `"error"` ではなく `"queued"` にする。フラグは `resetForRequeue` でクリアする（残すと次回の `Wait` エラーが誤って再キュー扱いになる）。

##### テスト可能プロパティ（PBT-01）

| 対象 | カテゴリ | プロパティ |
|---|---|---|
| `planStop` | Oracle | 停止対象数は `downloading` + `paused` の件数に一致する |
| `planStop` | Invariant（要素・順序保存） | 返り値は入力に含まれる `downloading` / `paused` のみで、入力順を保つ |
| `planStop` | Invariant（対応関係） | `needsResume` が真になるのは `paused` のときだけ（崩れると停止が効かない） |
| `planStop` | Invariant（副作用なし） | 呼び出し前後で全アイテムの `Status` が変化しない |
| `computeUpdateImpact` | Invariant（範囲制約） | `Items` / `OtherProcs` は非負、`OtherProcs <= liveProcs`、`Items == len(planStop(items))` |
| `computeUpdateImpact` | Invariant（判定の一致） | `Affected()` が偽になるのは `Items == 0 && OtherProcs == 0` のときだけ（= 確認ダイアログを省略できる条件） |
| `resetForRequeue` | Idempotence | 2 回適用しても 1 回と同じ状態になる |
| `resetForRequeue` | Invariant | 適用後は必ず `"queued"` かつ進捗ゼロ・エラーなし・停止フラグなしで、`shouldRemoveWhenDone` が偽 |
| 登録簿 | Invariant | 登録・解除の順序に関わらず生存数が一致し、更新中は 1 件も登録されない |

### ffmpeg

映像・音声ストリームの結合およびリマックスに ffmpeg を使用する。

- Windows: **アプリ内ダウンロード**（`InstallFfmpeg`）。`os.UserConfigDir()/moviedl/ffmpeg.exe` に配置する。yt-dlp と同じ「起動後に取得」モデル。
- macOS: Homebrew の標準インストールパスを直接参照（macOS GUI アプリはシェルの PATH を継承しない）

ffmpeg の探索順（`ffmpegPath`）: 管理パス（設定フォルダ） → `exec.LookPath` → Homebrew 既定パス。

#### なぜ Windows で埋め込みをやめたか

**以前**: リリースバイナリに ffmpeg.exe を `embed` で同梱し、起動時に展開していた。

**問題**: 「exe の中に別の exe（PE）が丸ごと入っている」状態は Windows Defender 等のヒューリスティック誤検知（特に `!ml` 系）を強く誘発し、ビルド成果物が削除される事象が起きた。バイナリも巨大になる。

**対策**: ffmpeg を埋め込まず、yt-dlp と同様にアプリ内から取得する。これにより埋め込み PE が消え、バイナリが小さく素直になる。
**注意**: これは誤検知の一要因を除くだけで万能ではない（未署名＋ダウンローダ挙動という要因は残る）。根治は Windows コード署名。

#### Windows ffmpeg の取得（InstallFfmpeg）

- 取得元: `yt-dlp/FFmpeg-Builds` の `ffmpeg-master-latest-win64-gpl.zip`（GitHub Releases、HTTPS）。
- 完全性検証: 同リリースの `checksums.sha256`（`<hex>␣␣<filename>` 形式）から対象 zip のダイジェストを取り、ダウンロードした zip の SHA256 と照合する。`InstallYtDlp` と同じ `fetchExpectedSum` / `parseSums` を流用する。不一致なら配置しない。
- 展開: zip 内の `*/bin/ffmpeg.exe`（basename が `ffmpeg.exe` のエントリ）を `ffmpegZipEntry` で特定し、一時ファイルへ展開 → SHA 一致確認済みの実体のみ `os.Rename` で最終パスへ原子的に配置する。
- タイムアウト付き `http.Client` を使う（zip が大きいため余裕を持たせる）。
- アプリ内インストールは Windows のみ対応（`CanInstallFfmpeg` が `goruntime.GOOS == "windows"` を返す）。macOS は従来どおり Homebrew 案内。

### フォーマット選択

ffmpeg が利用可能な場合:

```
-f bestvideo+bestaudio/best --merge-output-format mp4
```

ffmpeg がない場合:

```
-f best[ext=mp4]/best
```

引数の組み立ては純粋関数 `buildYtDlpArgs(tmpBase, workDir, ffmpegLoc, url)` に分離し、
`TestBuildYtDlpArgs` でテストする（`runDownload` から呼ぶ）。

### ダウンロードの堅牢化

**問題**: DASH/HLS のような断片配信では、yt-dlp の既定動作は「取得に失敗した断片を
黙ってスキップして続行」（`--skip-unavailable-fragments` が既定）。このため一時的な
ネットワーク不調で**一部の断片が欠けたまま**ファイルが完成し、

- 再生がその地点で**止まる／カクつく／無音になる**
- yt-dlp は正常終了するため、本アプリは `finished`（成功）として扱い、ユーザーは欠損に気づかない

という症状が出る（「他の方法より途中で止まりやすい」の主因と推定）。

**対策**: `buildYtDlpArgs` で全ダウンロードに以下を常時付与する（ffmpeg 有無に関わらず）。

```
--retries 10
--fragment-retries 10
--abort-on-unavailable-fragment
--socket-timeout 30
```

- `--abort-on-unavailable-fragment` が肝。断片が取得不能なら**スキップせず中断**し、
  yt-dlp が非ゼロ終了 → `runDownload` のエラー分岐で `Status="error"` になる。
  「finished は進捗 100% で決めてはならない」「後処理失敗の握りつぶし防止」と同じ思想。
- `--retries` / `--fragment-retries` は一時障害をリトライで吸収（明示。値は既定と同じ 10 だが意図を残す）。
- `--socket-timeout` は死んだ接続を早期検出してリトライに回す。

**やってはいけない**: 堅牢化のつもりで `--fragment-retries infinite` にすると、恒久的に
取得不能な断片（403/404 等）でハングする。`--abort-on-unavailable-fragment` と組み合わせる
場合は**有限のリトライ回数**にすること（無限リトライだと中断条件に到達しない）。

**挙動変更の注意（デグレ観点）**: この対策により、従来は「欠損したまま成功」だった
ダウンロードが**エラー表示に変わる**。これは意図した改善（壊れたファイルを成功と偽らない）
だが、ユーザーには再ダウンロードを促す挙動になる点を理解しておくこと。

### m3u8 / HLS

要件は [requirements.md「m3u8（HLS）対応」](../requirements/requirements.md)。
実測の根拠は [feasibility-m3u8.md](../requirements/feasibility-m3u8.md)。

**前提（実測済み）: 素の VOD m3u8 URL は既存の経路でそのまま動く。** AES-128 暗号化 HLS も、
映像・音声が別レンディションの master playlist も、既存の `buildYtDlpArgs` の引数のままで成功する。
したがって **m3u8 専用のダウンロード経路を作ってはいけない。** 追加するのは以下 3 点の補強のみ。

#### 判定は 1 つの述語に集約する（`isM3U8URL`）

m3u8 向けの挙動（Referer 付与・保存名の生成・登録時の警告）は**すべて同じ述語で分岐する**。

```go
// パスが .m3u8 で終わるか（大小無視）。クエリ文字列は判定に含めない。
func isM3U8URL(raw string) bool
```

- 判定は `neturl.Parse` の `Path` に対して行う。**生文字列の `strings.HasSuffix` で判定してはいけない。**
  m3u8 の URL は `...master.m3u8?token=abc` のようにクエリ付きが普通で、生文字列では末尾が `.m3u8` にならない
- 述語を 1 つに集約する理由: 3 箇所が別々の判定を持つと、片方だけ直したときに挙動が食い違う

#### Referer は m3u8 URL に限って付与する

**やってはいけないこと:** 全ダウンロードに `--referer` を付ける。

**なぜか:** 既存の 1000 以上の対応サイトは現在 Referer なしで動作している。全 URL に送り始めると
それらの経路の挙動を変える（デグレ）。`isM3U8URL` が真のときだけ付与すれば、既存サイトの経路には一切触らない。

```go
// m3u8 URL のオリジン（scheme://host/）を返す。m3u8 でない・不正な URL なら "" を返す。
func refererFor(raw string) string
```

- `buildYtDlpArgs` は URL から自分で導出する（引数は増やさない）。純粋関数のまま保つため
- **導出結果も引数インジェクション対策を通す。** `refererFor` は `scheme://host/` の形しか返さないため
  構造上 `-` 始まりにはならないが、`isValidURL` と同じ検証（http / https かつホストあり）を通してから返す。
  「`--` 終端と多層で守る」という既存方針と同じ（上記「引数インジェクション対策（`--` 終端は必須）」の節を参照）
- 視聴ページが別ドメインのサイトには効かない。これは既知の限界として受け入れる

#### 保存名は URL だけから組み立てる（`m3u8FileName`）

**問題:** HLS の m3u8 にはタイトルのメタデータがない。そのため STEP2a の `--dump-json` が返す `title` は
**m3u8 のファイル名そのもの**（`master` / `index` / `playlist` / `chunklist`）になり、保存名が
`master.mp4` に集中する。2 本目以降は `uniqueDest` により `master (1).mp4` になって中身が判別できない。

```go
// ホスト名 + 意味のあるパス要素 + 日時 を "_" で連ねた拡張子なしの base 名を返す。
func m3u8FileName(raw string, now time.Time) string
```

- 構成: `<host>_<意味のあるパス要素>_<yyyymmdd-hhmm>`
  例: `https://vod.example.com/hls/ab12cd/master.m3u8` → `vod.example.com_ab12cd_20260921-0915`
- **「意味のあるパス要素」の決め方**: パス要素から最後の要素（`.m3u8` のファイル名）を除き、
  残りを**末尾から走査して、汎用語の集合に含まれない最初の要素**を採用する。見つからなければ省略する。
  汎用語の集合は `hls` / `stream` / `streams` / `media` / `video` / `videos` / `playlist` / `manifest` /
  `out` / `vod` とする（小文字化して比較）
- **クエリ文字列は保存名に含めない。** トークンが延々と付いた読めない名前になるうえ、
  ログのマスク方針（後述）と矛盾する
- 戻り値は拡張子を含めない。呼び出し側（`runDownload` STEP5）が既存どおり実体の拡張子を付ける
- 最終的に `sanitizeFilename` を通す経路は既存のまま（制御文字・Windows 予約名の処理を二重に書かない）

**適用箇所:** STEP2a の直後に、`isM3U8URL(item.URL)` が真なら `item.Title` を `m3u8FileName` の値で
**上書きする**。STEP2a のタイトル事前取得そのものは残す（到達性の確認とログに価値があり、流れを変えない方が安全）。

**なぜ「title が汎用語のときだけ上書き」にしないか:** パスが `.m3u8` で終わる URL は generic
エクストラクターが処理し、`title` は必ず m3u8 のファイル名になる（実測）。条件を足すと
「どちらの名前が使われるか」が URL によって変わり、純粋関数で決まらなくなる。

#### ログにトークン付き URL を残さない（`redactLine`）

**やってはいけないこと:** yt-dlp のコマンド行や出力行をそのままログに書く。

**なぜか:** `moviedl.log`（`os.UserConfigDir()/moviedl/` 内・0644・平文）には実行コマンド全体と
yt-dlp の出力行が追記される。m3u8 の URL は**有効期限トークンが実質的な認可情報**であり、
クエリ文字列ごと平文で残るのは機微情報の出力にあたる（SECURITY-03）。

```go
// 文字列中の http(s) URL のクエリ部分を "?<redacted>" に置き換える。
func redactLine(s string) string
```

- 適用箇所は **`logf` に渡す前**の 2 箇所: STEP2 のコマンド行、STEP3 の yt-dlp 出力行
- URL の**パスは残す**（どのファイルで失敗したかの調査に必要）。落とすのはクエリだけ
- 文字列操作のみで実装し `neturl.Parse` に依存しない（パース不能な行でも確実にマスクするため）
- **冪等**であること（二重適用しても結果が変わらない）。ログ経路が増えても壊れない

#### 期限切れ URL

m3u8 の URL は有効期限トークン付きのことが多く、失効すると 403 になる。**技術的な対策はない。**

- 同時ダウンロード数 0（登録のみモード）で m3u8 を登録したときに、フロントエンドが警告を出す
- 403 で失敗したときのエラー文言で、URL 失効の可能性と取り直しを案内する
- **`maxActive == 0` に m3u8 の特別分岐を足してはいけない**（「maxActive == 0（登録のみモード）」の節を参照）。
  警告は表示のみで、開始の判断はユーザーに残す

#### テスト可能プロパティ（PBT-01）

追加する純粋関数はいずれも「任意の URL 文字列」を入力に取るため、ランダム生成した URL に対する
不変条件の検証価値が高い。ジェネレータは URL の構成要素（スキーム・ホスト・パス要素・クエリ）から
**構造的に妥当な URL を組み立てる専用ジェネレータ**を用意する（PBT-07。生の文字列を URL として渡さない）。

| 対象 | カテゴリ | プロパティ |
|---|---|---|
| `isM3U8URL` | Invariant（クエリ非依存） | 同じ URL にクエリを足しても判定結果が変わらない（`...master.m3u8` と `...master.m3u8?token=x` は同じ判定） |
| `isM3U8URL` | Invariant（大小無視） | パス末尾の `.m3u8` / `.M3U8` / `.M3u8` は同じ判定になる |
| `refererFor` | Invariant（引数インジェクション） | **戻り値は決して `-` で始まらない**。任意の入力で成立すること |
| `refererFor` | Invariant（対応関係） | 非空を返すのは `isM3U8URL` が真かつ `isValidURL` が真のときだけ |
| `refererFor` | Invariant（オリジンのみ） | 戻り値にパス要素・クエリ・フラグメントが含まれない |
| `m3u8FileName` | Invariant（安全なファイル名） | `sanitizeFilename` を通した結果が元と一致する（= 既に安全）。パス区切り・制御文字を含まない |
| `m3u8FileName` | Invariant（機微情報） | **戻り値にクエリ文字列由来の文字が含まれない**（トークンが保存名に漏れない） |
| `m3u8FileName` | Invariant（非空） | 任意の m3u8 URL に対し空文字を返さない（空だと `runDownload` が `tmpBase` のままの名前で保存してしまう） |
| `redactLine` | Idempotence | `redactLine(redactLine(s)) == redactLine(s)`。ログ経路が増えても二重適用で壊れない |
| `redactLine` | Invariant（マスクの網羅） | 出力に、入力の URL が持っていたクエリ文字列がそのまま現れない |
| `redactLine` | Invariant（保存） | URL を含まない行は一切変化しない |
| `isKillablePID` | Invariant（範囲制約） | 真を返すのは `pid > 1` のときだけ（詳細は「プロセス管理」の節） |

PBT は例示ベーステストを置き換えず併存させる（PBT-10）。ファイルは既存どおり `pbt_test.go` に分離する。

#### 対象外

| 項目 | 理由 |
|---|---|
| ライブ配信の録画（停止してそこまでを保存） | 中断時の出力が 0 バイト・再生不能になることを実測で確認（「プロセス管理」の節を参照） |
| ローカルの .m3u8 ファイル | 対象はインターネット上の URL（ユーザー判断） |
| DRM（Widevine / FairPlay / PlayReady） | 復号できない。`has_drm` で判別可能 |

---

## ダウンロード状態管理

### 単一リストモデル

`App` 構造体は `items []*DownloadItem` の単一リストでアイテムを管理する（順序＝表示順序）。  
各アイテムの状態は `Status` フィールドで表す。チャネルベースの単一ワーカーは廃止する。

```go
type App struct {
    ctx       context.Context
    mu        sync.Mutex
    items     []*DownloadItem  // 登録順に並ぶ単一リスト
    schedCh   chan struct{}     // 状態変化通知用（バッファ 1）
    maxActive int               // 自動補充で維持する実行中アイテム数の上限（0〜10、既定 0）
}
```

`maxActive` は `mu` で保護する。`SetMaxConcurrent(n int)` で更新（**0〜10 にクランプ**）したのち `notify()` で scheduler を起こす。`GetMaxConcurrent() int` でフロントエンドの初期値を返す。

**既定値は 0（`NewApp` で `maxActive: 0`）。** 設定は永続化しないため、起動するたびに 0 に戻る。起動直後は自動補充が行われず、ユーザーがプルダウンで 1 以上を選ぶか手動開始するまでダウンロードは始まらない。

`DownloadItem.Status` の取り得る値:

| 値 | 意味 |
|---|---|
| `"queued"` | 待機中 |
| `"downloading"` | ダウンロード中 |
| `"paused"` | 一時停止中 |
| `"finished"` | 完了 |
| `"error"` | エラー |
| `"cancelled"` | キャンセル済み |

### 自動補充ルール（scheduler）

`scheduler()` goroutine が `schedCh chan struct{}` を監視する。  
状態変化が発生するたびに `schedCh` へ通知を送る（バッファ 1 なので重複通知はまとめられる）。  
`scheduler` は通知を受けるたびに以下を評価する:

```
active = items の中で Status == "downloading" の件数
items を先頭から走査し、active < maxActive である限り
  Status == "queued" のアイテムを "downloading" に変更し goroutine を起動、active++
```

`active >= maxActive` であれば何もしない。`maxActive == 1` のときは従来どおり「実行中が 0 件のとき先頭の待機アイテムを 1 件だけ起動」という挙動になる（後方互換）。

**注意:** 起動対象のアイテムは `mu` ロック下で `"downloading"` に確定させてから、ロックを解放した上で `runDownload` goroutine を起動すること。1 回の通知で複数件を起動しうるため、スライスにためてからまとめて起動する。ロック保持中に goroutine を起動したり `emit` を呼んだりしない（`runDownload` 冒頭でも `mu` を取るためデッドロックの原因になる）。

手動開始（`StartDownload`）は `maxActive` の制約を受けない。設定値を超えて並行ダウンロードを開始できる。

### maxActive == 0（登録のみモード）

`maxActive == 0` は「自動補充を一切行わない」状態を表す。要件は [requirements.md「同時ダウンロード数 0（登録のみモード）」](../requirements/requirements.md) を参照。

**実装上のポイント:**

- `selectToStart` は先頭の `if active >= maxActive { break }` により、`maxActive == 0` なら `active == 0` でも即 break して `nil` を返す。**0 のための特別分岐を追加してはいけない。** 一般ルール（`active < maxActive` の間だけ補充）がそのまま 0 を包含しており、分岐を足すと二重の真実源になる
- 抑止するのは **scheduler による自動起動だけ**。`AddToQueue` / `FetchVideoInfo` / プレイリスト一覧取得 / 重複チェック / `StartDownload`（手動開始）/ `ResumeDownload` は `maxActive` を参照しないので、0 でもそのまま動作する。**これらの経路に `maxActive == 0` のガードを入れてはいけない**（要件で明示的に許可されている）
- 0 に切り替えても実行中のアイテムは停止させない。scheduler は「起動する」方向にしか作用しないため、`SetMaxConcurrent(0)` は `notify()` を送るだけで実行中には何も起きない。これは引き下げ全般（例: 5 → 2）と同じ挙動で、**意図的な設計**である
- 0 → 1 以上に引き上げたときは `SetMaxConcurrent` 末尾の `notify()` により待ちキューから即座に補充される（既存の引き上げ経路と同一）

**なぜ既定を 0 にするか:** 起動直後に前回のキューが勝手に走り出すのを避け、ユーザーが明示的に開始するまで何も始まらない状態を既定にするため。したがって `NewApp` の `maxActive` を 1 に戻してはいけない。

### テスト可能プロパティ（PBT-01）

Property-Based Testing 拡張（opt-in / Full 強制）に基づき、`maxActive` 周りのテスト可能プロパティを以下のとおり特定する。PBT フレームワークは **`pgregory.net/rapid`**（PBT-09。カスタムジェネレータ・自動 shrink・シード再現に対応し `go test` に統合される）。

| 対象 | カテゴリ | プロパティ |
|---|---|---|
| `SetMaxConcurrent` | Invariant（範囲制約） | 任意の `int` 入力に対し、適用後の `GetMaxConcurrent()` は常に `0 <= n <= 10` |
| `SetMaxConcurrent` | Invariant（恒等） | `0 <= n <= 10` の入力はクランプされずそのまま保持される |
| `SetMaxConcurrent` | Idempotence | `Set(n); Set(n)` の結果は `Set(n)` と同じ（クランプは冪等） |
| `selectToStart` | Oracle（件数の参照計算） | 返り値の件数は常に `min(queued 件数, max(0, maxActive - downloading 件数))` |
| `selectToStart` | Invariant（範囲制約） | `downloading 件数 + len(返り値) <= max(maxActive, downloading 件数)`。とくに `maxActive == 0` では常に空 |
| `selectToStart` | Invariant（要素保存） | 返り値は入力に含まれる `"queued"` アイテムのみで、入力順を保つ |
| `selectToStart` | Invariant（副作用なし） | 呼び出し前後で全アイテムの `Status` が変化しない |

ジェネレータは `[]*DownloadItem` を「実在する `Status` 値のみ」から生成する専用ジェネレータを用意する（PBT-07。生の文字列を `Status` に入れない）。PBT は例示ベーステストを置き換えず併存させる（PBT-10）。ファイルは `pbt_test.go` に分離する。

### キュー登録（AddToQueue）と重複防止

`AddToQueue(url, outputDir)` は URL をキューへ登録するが、**既存アイテムと同一 URL の重複登録を防ぐ**。

- `a.mu` ロック下で `addRejection(a.items, url)` を評価する。不正 URL または重複なら**登録せず、拒否理由と表示文言を `AddResult` で返す**（呼び出し側は必ずユーザーへ提示する。下記「拒否した理由を返す（沈黙させない）」を参照）。
- 比較は完全一致。単一動画は `entries[0].url`、プレイリストは各 `entry.url`（いずれも yt-dlp が返す正規 URL）が `DownloadItem.URL` に入るため、生入力の表記揺れに依らず正規 URL 同士で判定できる。
- 重複判定の対象は `items` に現存する全アイテム（queued / downloading / paused / error）。完了（finished）・キャンセル（cancelled）は `items` から除去済みのため自然に対象外。
- **重複チェックと append は同一ロック区間で行う**こと。Wails の各 IPC 呼び出しは別 goroutine で走るため、同一 URL の同時登録が二重に通るのを防ぐ。
- 判定は純粋関数 `containsURL([]*DownloadItem, string) bool` に切り出してテストする。

エラー状態の同一 URL を再実行したい場合は「リトライ」ボタンを使う（重複登録ではなく既存アイテムの再キュー）。

#### 拒否した理由を返す（沈黙させない）

**やってはいけないこと:** 登録を拒否したときに空文字だけを返し、呼び出し側がそれを無視する。

**なぜか（2026-09-23 に実際に起きた）:** ユーザーが URL を登録したところ、ダウンロードが
開始されないままリストから消えた。フロントエンドは「取得中…」プレースホルダーを
`finally` で必ず消すため、**拒否された場合は何も残らず、エラーも出ない**。
`AddToQueue` は理由を返さず、登録経路はログも書いていなかったため、
**ユーザーも開発者も原因を特定できなかった。**

「重複は静かにスキップ」という判断自体は妥当だが、それが**不具合と見分けられない**のが問題。

**正しい代替手段:** 拒否理由を構造化して返し、判定を純粋関数に切り出す。

```go
type AddResult struct {
    ID      string `json:"id"`      // 受理時のみ
    Reason  string `json:"reason"`  // "" = 受理 / "invalid" / "duplicate"
    Message string `json:"message"` // 拒否理由の表示文言（受理時は ""）
}

// addRejection は拒否すべきかとその理由を返す。"" なら受理。
func addRejection(items []*DownloadItem, url string) string
func addRejectionMessage(reason string) string
```

- **判定と append は従来どおり同一ロック区間**で行う（同一 URL の同時登録を防ぐため）。
  `addRejection` を純粋関数にしてロック外でテストできるようにする
- フロントエンドは `reason` が非空なら `message` を表示する。登録処理は止めない
- 拒否は必ずログにも記録する（下記「ログのセッション管理」）

### ログのセッション管理

**やってはいけないこと:** アプリ起動時に `truncateLog` でログを空にする。

**なぜか:** 問題を再現した後にアプリを再起動すると**証拠が消える**。実際に上記の事象で、
再現後に確認した時点でログは 0 バイトだった。「再現してから再起動しないでください」と
ユーザーに要求するのは調査手順として現実的でない。

**正しい代替手段:** 起動時は追記を続け、**上限を超えたときだけ 1 世代退避する**。

```go
const maxLogBytes = 5 << 20 // 5 MiB

func shouldRotateLog(size int64) bool { return size >= maxLogBytes }
```

- 上限超過時は `moviedl.log` を `moviedl.log.1` へ `os.Rename`（前回世代は上書きされる）。
  ログを消す経路はここだけにする
- 起動ごとにセッション開始行（バージョン付き）を書き、どこから新しい実行かを判別できるようにする
- 追記は `appendLog` に集約する。`runDownload` の高頻度な進捗行は従来どおり
  開いたままのハンドル（`logf`）を使い、低頻度のイベント（登録・拒否・取得）は `appendLog` を使う
- `appendLog` も `logLine` 経由にする（トークンのマスクを通すため）

### Referer のユーザー指定（元ページ URL）

自動導出（`refererFor`）は m3u8 自身のオリジンを返すため、**プレイヤーや CDN が視聴ページと
別ドメインにある構成では効かない**。エラーになったアイテムに対して、ユーザーが元ページの
URL を指定して再試行できるようにする。

```go
// effectiveReferer はユーザー指定を優先し、なければ m3u8 の自動導出に落ちる。
func effectiveReferer(url, override string) string

func (a *App) RetryWithReferer(id, pageURL string) string // "" = 成功、非空 = エラー文言
```

- `DownloadItem.Referer` に保持する。**`resetForRequeue` / リトライで消してはいけない**
  （再試行のために指定した値が消えたら意味がない）
- 指定値も `isValidURL` を通す。**通らなければ受け付けずエラー文言を返す**
  （`-` 始まりの値が `--referer` の引数に化けるのを防ぐ。「引数インジェクション対策」と同じ）
- ユーザー指定は **m3u8 以外の URL にも適用する**。自動付与を m3u8 に限定したのは既存経路の
  挙動を変えないためで、ユーザーが明示したものはその限定の対象外
- `buildYtDlpArgs` は Referer の上書き値を引数で受け取る（純粋関数のまま保つ）
- `RetryDownload` と `RetryWithReferer` は `resetForRetry` を共有する（初期化漏れを 1 箇所に集める）

### テスト可能プロパティ（PBT-01）— 診断性と Referer 指定

| 対象 | カテゴリ | プロパティ |
|---|---|---|
| `addRejection` | Oracle（判定の一致） | 受理するのは「`isValidURL` が真かつ `containsURL` が偽」のときだけ。不正判定が重複判定より先 |
| `addRejectionMessage` | Invariant（沈黙の禁止） | **`reason` が非空なら文言も必ず非空**。未知の理由でも空にしない（この関数の存在理由そのもの） |
| `addRejectionMessage` | Invariant（対応関係） | `reason` が空のときだけ文言が空 |
| `shouldRotateLog` | Invariant（範囲制約・単調） | 真になるのは `size >= maxLogBytes` のときだけで、真なら `size+1` でも真 |
| `effectiveReferer` | Invariant（引数インジェクション） | **任意の URL と任意の指定値に対し `-` で始まらない** |
| `effectiveReferer` | Invariant（優先順位） | 妥当な指定値は必ず採用され、自動導出に勝つ |
| `effectiveReferer` | Invariant（フォールバック） | 指定が空・不正なら `refererFor(url)` と一致する |

### 手動開始（StartDownload）

`StartDownload(id string)` を JS から呼ぶと:

1. `items` の中から該当アイテムを探す（Status が `"queued"` または `"paused"` であること）
2. Status を `"downloading"` に変更し、ダウンロード goroutine を起動する
3. `schedCh` に通知（scheduler は `"downloading"` が存在するため何もしない）

### ダウンロード完了時

`runDownload` goroutine が終了するとき（成功・失敗・キャンセル問わず）:

1. Status を最終状態（`"finished"` / `"error"` / `"cancelled"`）に更新する
2. `schedCh` に通知する（`"downloading"` が 0 件になった場合、scheduler が次を自動起動）
3. リストからの自動削除は **成功 (`finished`) と キャンセル (`cancelled`) のみ**。エラー (`error`) はリストに残す

### エラー終了したアイテムの扱い

**やってはいけないこと:**

- エラー終了時に `a.removeItem` を呼んでリストから消す
- フロントエンドの `download:update` ハンドラーで `error` を terminal 扱いして DOM から消す

**理由:** エラーはディスクフル・一時的なネットワーク障害など、ユーザーの対処で復旧可能なケースが多い。自動消去すると原因表示も失われ、ユーザーが何が起きたか分からなくなる。過去の不具合再発防止のためここに固定する。

**正しい設計:**

- `runDownload` の最終段は `if item.Status != "error" { a.removeItem(item.ID) }`
- フロントエンドの terminal 判定は `finished` と `cancelled` のみ。`error` はそのまま `downloads[item.id]` に残す
- エラーアイテムには「リトライ」「削除」の 2 つのアクションボタンを表示する

#### `finished` は進捗 100% で決めてはならない（後処理失敗の握りつぶし防止）

**やってはいけないこと:** `parseYtDlpLine` で進捗が 100% に達したら `item.Status = "finished"` にする。

**なぜか:** `bestvideo+bestaudio` では「①映像DL → ②音声DL → ③ffmpeg 結合（後処理）」の順で進む。①②のダウンロードは 100% に達するが、その後 ③ の後処理（`ERROR: Postprocessing: Conversion failed!` など）で失敗し得る。進捗 100% で `finished` にすると、`cmd.Wait()` がエラーを返しても [エラー処理のガード `else if item.Status != "finished"`] に弾かれて `error` に遷移せず、**失敗したのに成功扱いでリストから自動削除される**（ユーザーから見るとファイルが出来ていないのに黙って消える）。

**正しい設計:**

- `parseYtDlpLine` は **進捗率（`Percent`）の更新だけ**を行い、`Status` を `finished` にしない。
- 完了の確定は `runDownload` の成功分岐（`cmd.Wait()` が `nil` を返し、STEP4/5 のファイル移動まで済んだとき）でのみ `item.Status = "finished"` とする。
- これにより後処理失敗は `cmd.Wait()` エラー → `error` に遷移し、リストに残ってリトライ/削除できる。

### リトライ（RetryDownload）

エラー状態のアイテムを再実行するための API:

```go
func (a *App) RetryDownload(id string)
```

1. `items` から該当アイテムを探す（Status が `"error"` であること）
2. Status を `"queued"` に戻し、`Error` / `Percent` / `Speed` / `ETA` / `Elapsed` / `cancelFlag` をクリアする
3. `emit` でフロントエンドに通知し、`notify()` で scheduler を起こす（実行中が 0 件なら自動補充される）

`item.cmd` は新しい `runDownload` の冒頭で上書きされるのでクリア不要。`workDir` は前回の `runDownload` の defer で削除済みなので、新しい workDir が `runDownload` 内で作られる。

### エラーアイテムの削除（CancelDownload の拡張）

「×」ボタン押下時の `CancelDownload(id)` は以下の状態を扱う:

| 元の Status | 動作 |
|---|---|
| `"queued"` | Status を `"cancelled"` にして即削除 |
| `"paused"` | プロセスを Kill → Status を `"cancelled"` → 削除 |
| `"downloading"` | プロセスを Kill（goroutine 終了時に削除される） |
| `"error"` | Status を `"cancelled"` にして即削除（ユーザーによる明示的な dismiss） |

---

## 一時停止・再開

### 一時停止（PauseDownload）

1. 該当アイテムの Status を `"paused"` に変更する
2. プロセスをサスペンドする（プラットフォーム別）
3. `schedCh` に通知する（`"downloading"` が 0 件になった場合、scheduler が次を自動起動）

### 再開（ResumeDownload）

1. 該当アイテムの Status を `"downloading"` に変更する
2. プロセスをレジュームする（プラットフォーム別）

### プラットフォーム別サスペンド実装

| プラットフォーム | 一時停止 | 再開 |
|---|---|---|
| macOS / Linux | `cmd.Process.Signal(syscall.SIGSTOP)` | `cmd.Process.Signal(syscall.SIGCONT)` |
| Windows | `NtSuspendProcess` (syscall 経由) | `NtResumeProcess` (syscall 経由) |

実装は `sysproc_windows.go` / `sysproc_other.go` に分けて定義する。
**ただし対象は単一プロセスではなくプロセスツリーである**（次節）。

---

## プロセス管理（停止は孫プロセスまで及ばせる）

**この節は 2026-09-21 の m3u8 実現可能性評価で発見した既存不具合への対策である。**
経緯と実測ログは [feasibility-m3u8.md](../requirements/feasibility-m3u8.md)「注目点 3」を参照。

### ライブ HLS は ffmpeg に委譲される（＝ yt-dlp は末端プロセスではない）

**やってはいけないこと:** `cmd.Process.Kill()` や `cmd.Process.Signal(SIGSTOP)` で
**yt-dlp のプロセスだけ**を停止して、それで止まったと考える。

**なぜか:** yt-dlp は処理内容によって **ffmpeg を子プロセスとして起動し、そちらに処理を丸投げする**。

| 委譲が起きる場面 | ffmpeg の生存時間 |
|---|---|
| **ライブ HLS のダウンロード** | **配信が続く限り数時間**（`-c copy -f mpegts` で `.part` に書き続ける） |
| 映像・音声の結合（`[Merger]`） | 数秒〜数十秒 |
| MPEG-TS の MP4 コンテナ修正（`[FixupM3u8]`） | 数秒 |

そして `applyOSProcAttr` は**非 Windows では no-op**で、プロセスグループを作っていなかった。
Windows 側も `HideWindow: true` だけで Job Object を使っていなかった。
このため **SIGKILL は yt-dlp にしか届かず、ffmpeg は `PPID 1` の孤児として生き残り処理を続ける**
（実測で確認。yt-dlp PID 57658 を SIGKILL → 子 ffmpeg 57660 が生存継続）。

現状の症状:

| 操作 | 実際に起きていたこと |
|---|---|
| キャンセル | yt-dlp だけ死に、ffmpeg が裏で帯域を食い続ける。`workDir` を `RemoveAll` しても書き込みが続く |
| 一時停止 | yt-dlp だけ止まり、**ffmpeg はダウンロードを続ける**（止まっていない） |
| yt-dlp の更新 | `waitProcsDrained` は yt-dlp の生存だけを見るため、**ffmpeg が実行ファイルを掴んだままでも「排出完了」と判定する**（Windows では置き換えが共有違反で失敗しうる） |

**これは m3u8 固有でもライブ固有でもない。** VOD でも `[Merger]` / `[FixupM3u8]` の最中にキャンセルすれば
同じ経路を通る（後処理が短命なので目立たなかっただけ）。

### 正しい代替手段: OS ごとのプロセスツリー抽象

OS に依存しない側（`procs.go` / `download.go` / `queue.go`）から OS 差分を見えなくするため、以下の関数を `sysproc_*.go` に定義する。
**`suspendProcess` / `resumeProcess`（単一プロセス版）は廃止し、ツリー版に置き換える。**

```go
func applyOSProcAttr(cmd *exec.Cmd)          // Start 前。非 Windows: Setpgid / Windows: HideWindow
func trackProcessTree(cmd *exec.Cmd)         // Start 直後。Windows: Job Object を作って割り当て。非 Windows: no-op
func killProcessTree(cmd *exec.Cmd) error    // ツリー全体に SIGKILL / TerminateJobObject
func suspendProcessTree(cmd *exec.Cmd) error // ツリー全体を中断
func resumeProcessTree(cmd *exec.Cmd) error  // ツリー全体を再開
func releaseProcessTree(cmd *exec.Cmd)       // Wait 後。Windows: ジョブハンドルを閉じる。非 Windows: no-op
```

**`applyOSProcAttr` と同じ「1 箇所でも漏れたらその経路だけ穴が空く」ルールである。**
`exec.Command` を追加するときは `applyOSProcAttr` → `Start` → `trackProcessTree` の順を必ず通し、
停止はツリー版の関数を使うこと。

### ⚠️ 非 Windows: `Kill(-pid)` の符号は致命的に危険

**やってはいけないこと:** ガードなしで `syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)` を呼ぶ。

**なぜか:** `kill(2)` の第 1 引数の意味は符号と値で変わる。

| 引数 | 意味 |
|---|---|
| `pid > 0` | そのプロセスのみ |
| `-pgid`（`pgid > 1`） | そのプロセスグループの全員 ← **これが狙い** |
| **`-1`** | **呼び出し元が権限を持つ全プロセス** ← **アプリごと巻き込んで殺す** |
| `0` | 呼び出し元自身のプロセスグループ ← **アプリ自身を殺す** |

`cmd.Process` が `nil` だったり、`Pid` が 0 / 1 になっている経路で符号を反転させると、
`kill(0, ...)` や `kill(-1, ...)` が成立しうる。これは requirements.md のセキュリティ要件
「停止対象をそのダウンロードのために起動したプロセスに限定する」に直接違反する。

**正しい代替手段:** 符号を反転させる前に必ずガードする。判定は純粋関数に切り出してテストで固定する。

```go
// ツリー停止の対象として正当な pid か。1 以下は拒否する。
// Unix ではこの pid を pgid として符号反転に使い、Windows ではジョブから
// 列挙した pid の妥当性確認に使う（両 OS 共通なので procs.go に置く）。
func isKillablePID(pid int) bool { return pid > 1 }
```

`Setpgid: true` を付けた子は**自分自身がグループリーダー**になるため `pgid == cmd.Process.Pid` が成り立つ。
別途 `Getpgid` を引く必要はない（引けば、プロセスが既に消えていたときにエラーで分岐が増える）。

### Windows: Job Object と、割り当ての競合窓

- `CreateJobObjectW` でジョブを作り、`JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` を設定して
  `AssignProcessToJobObject` で yt-dlp を入れる。停止は `TerminateJobObject` でツリー全体に及ぶ
- 中断・再開はジョブに機能がないため、`QueryInformationJobObject`（`JobObjectBasicProcessIdList`）で
  ジョブ内の PID を列挙し、各 PID に既存の `NtSuspendProcess` / `NtResumeProcess` を適用する
- **既知の競合窓:** 割り当ては `cmd.Start()` の**直後**に行うため、Start から Assign までの
  わずかな間に yt-dlp が子を産むと、その子はジョブに入らない。実際には yt-dlp（Python）の起動に
  時間がかかり ffmpeg を産むのはその後なので実用上は問題にならない。厳密に閉じるには
  `CREATE_SUSPENDED` で起動してから割り当てて再開する必要があるが、`os/exec` は
  スレッドハンドルを公開しないため現状の Go 標準ライブラリでは実装できない
- 既存の `HideWindow: true` は維持する（コンソールウィンドウ抑止。別の節を参照）

### ライブを停止すると成果物は残らない（`.part` が 0 バイトになる）

**やってはいけないこと:** 「ライブを停止したらそこまでの分が保存される」と期待する実装・UI にする。

**なぜか:** ライブでは ffmpeg が `.part` に書いており、`SIGKILL` / `TerminateJobObject` では
**出力バッファがフラッシュされない**。実測では停止後の `.part` は **0 バイト**で、
`ffprobe` が `Invalid data found` を返す（救出不能）。

対照的に **VOD（hlsnative）で中断した `.part` は救出できる**。実測では 59,220 バイトの valid な
MPEG-TS で、`ffprobe` が読めて `ffmpeg -c copy` で再生可能な mp4 になった。

したがって「停止してそこまでを保存」を実装するなら、ライブについては
**`SIGKILL` ではなく `SIGINT` / `SIGTERM`（または stdin へ `q`）を孫の ffmpeg に届ける**必要がある。
**現時点ではライブの録画は対象外**であり、この非対称性を知らずに「停止して保存」を作ると
ライブだけ 0 バイトのファイルが残る実装になる。

### テスト可能プロパティ（PBT-01）

| 対象 | カテゴリ | プロパティ |
|---|---|---|
| `isKillablePID` | Invariant（範囲制約） | 任意の `int` に対し、真を返すのは `pid > 1` のときだけ。とくに `0` / `1` / 負数では必ず偽 |

---

## プレイリスト・ファイル選択

### フロー

1. ユーザーが URL を入力して「追加」を押す
2. フロントエンドは入力欄を即座にクリアし、取得処理を **非同期に** 開始する。「追加」ボタンは無効化しない（後続 URL の入力をブロックしない）
3. 取得中の URL はダウンロードリスト上部に「取得中…」プレースホルダーアイテムとして表示する
4. Go 側で `yt-dlp --flat-playlist --dump-json --no-download URL` を実行する（Wails の各 IPC 呼び出しは個別の goroutine で動くため、複数の取得は Go 側で自然に並列化される）
5. 単一動画の場合は直接 `items` の末尾に追加する。複数エントリの場合はエントリ一覧（ID・タイトル・サムネイル等）を JS に返し、選択モーダルを表示する
6. 「キューに追加」を押すと選択されたエントリが個別の `DownloadItem` として `items` の末尾に追加される
7. 取得が完了したら、対応する「取得中…」プレースホルダーは UI から除去する

### 非同期取得の設計上の要点

- **「追加」ボタンを無効化してはならない / `await` で UI をブロックしてはならない**: 取得は数秒〜数十秒かかるため、待ちが発生すると体験が悪化する。
- **モーダルの直列化**: プレイリスト選択モーダルは同時に 1 つしか開かない。複数の取得がほぼ同時にプレイリストを返した場合、後続のモーダルはキューイングし、現在のモーダルが閉じた後に順次表示する。
- **取得中アイテムの識別**: フロントエンド内部のローカル ID（`f1`, `f2`, ...）で管理し、Go 側の `DownloadItem.ID` とは独立した名前空間にする。Go 側のリストには取得完了後にしか追加されない。
- **エラー表示**: 取得失敗時はプレースホルダーを除去したうえでエラーを通知する（alert / トーストなど）。

### ドラッグ&ドロップ入力

- **ドラッグ中だけ最前面・ウィンドウ全面のオーバーレイ div でドロップを受ける。** これが最も確実。
  - 仕組み: 既定は `display:none` / `pointer-events:none` の `#drop-overlay`（`position:fixed; inset:0; z-index` 最大）を用意し、`dragenter` / `dragover` で `active` クラスを付けて `display:flex` / `pointer-events:auto` にして前面化、`drop` と（ウィンドウ外への）`dragleave` で外す。
  - **なぜオーバーレイが必要か（capture だけでは不十分）:** `window` のキャプチャ購読で `preventDefault()` しても、**ネイティブ部品（`<select>` プルダウン・`<input>` など）や WebView のドロップ経路**では、OS レベルのドロップがその部品へ先に渡り、HTML の `drop` を経ずにネイティブ遷移が起きる領域が残る。オーバーレイを最前面に被せると、ドラッグ中はカーソル下の要素が常にこのオーバーレイ div になり、ドロップ先がこの div に固定される＝下のネイティブ部品が OS のドロップを受ける経路を**物理的に塞げる**。
  - **やってはいけないこと:** ドロップ受け口を URL 入力欄だけに付ける／オーバーレイ無しで個別要素の `drop` だけに頼る。落とす位置がシビアになり、欄外やネイティブ部品上で WebView の既定動作（ドロップ URL へ遷移＝別表示）が起きる。
- **二重の防御として `window` の `dragenter`/`dragover`/`drop`/`dragleave` もキャプチャフェーズ（`addEventListener(..., true)`）で購読し `preventDefault()` する。** キャプチャは要素より先に発火するため、オーバーレイ前面化が間に合わない一瞬の取りこぼしも抑止できる。
- `dragover` で `preventDefault()` を呼ばないと `drop` が発火せず既定動作（遷移）になる。`dropEffect = 'copy'`。`dragenter` でも `preventDefault()`。
- `drop` で `event.preventDefault()` を**必ず**呼ぶ（URL を含まないドロップでも遷移阻止のため）。
- ハンドラはウィンドウ／オーバーレイに一本化し、入力欄個別の `ondrop` は付けない（二重処理回避）。
- ドロップデータの取得優先順位: `text/uri-list` → `text/plain`
- `text/uri-list` は RFC 2483 に従い改行区切りで複数 URL を含み、`#` で始まる行はコメント。これらを除外する
- `text/plain` も改行区切りで複数行入力を許容する
- 各 URL は通常の `submitURL(url)` 経路を辿る（取得中プレースホルダー追加 → 非同期取得 → キュー追加 / モーダル）。「追加」ボタン押下と区別しない
- ドロップ後は入力欄を空のままにする（押下と同等の状態）

### Go 側 API

```go
// FetchPlaylist はURLの内容を返す。単一動画の場合は1件のスライス。
func (a *App) FetchPlaylist(url string) ([]PlaylistEntry, error)

type PlaylistEntry struct {
    ID        string `json:"id"`
    URL       string `json:"url"`
    Title     string `json:"title"`
    Thumbnail string `json:"thumbnail"`
    Duration  string `json:"duration"`
}
```

---

## キャンセル

- `items` のいずれのアイテムもキャンセルできる
- `"queued"`: Status を `"cancelled"` に更新するだけ（プロセスなし）
- `"paused"`: プロセスを `Kill()` してから Status を `"cancelled"` に更新する
- `"downloading"`: `cmd.Process.Kill()` で強制終了する（goroutine 終了時に Status が更新される）

---

## ファイル管理

### 作業ディレクトリ

- ダウンロードごとに保存先フォルダ内に作業ディレクトリ（`.moviedl-work-XXXX`）を作成する
  - プレフィックスは定数 `workDirPrefix`（`.moviedl-work-`）。`os.MkdirTemp` のパターンに使う
- 保存先フォルダ内に置く理由:
  - クラッシュ時にユーザーが可視・手動削除できる
  - 保存先と同一ファイルシステムのため、ファイル移動が確実

#### workDir 削除はプレフィックス検証必須

起動時 `cleanupLeftoverWorkDirs` は `workdirs.json` に記録された残骸 workDir を `os.RemoveAll` で
掃除する。**`workdirs.json` のパスを無検証で `os.RemoveAll` してはならない。** このファイルは
ユーザー設定フォルダ内の平文（0644）で、改竄やバグで不正なパス（例: ホームディレクトリ）が
混入すると任意ディレクトリを再帰削除しうる。**必ず `isManagedWorkDir`（basename が `workDirPrefix`
始まりか）で検証し、通過したパスだけを削除する。** 判定は `TestIsManagedWorkDir` で固定。

### yt-dlp への指示

```
-o <ランダム8バイト16進数>.%(ext)s  ← 起動ごとに生成したランダム文字列（衝突ゼロ）
-P home:<作業ディレクトリ>           ← 最終ファイルの出力先
-P temp:<作業ディレクトリ>           ← .part ファイルの置き場
-P infojson:<作業ディレクトリ>       ← .info.json の置き場
--write-info-json                    ← タイトル取得用 JSON を書き出す
```

すべてのパスを `workDir` に向けることで、yt-dlp は `outputDir` を一切参照しない。
`-o` にランダム文字列を使うことで、info.json を含むいかなる既存ファイルとも衝突しない。

### yt-dlp 実行中に workDir に作られるファイル

**ffmpeg あり（bestvideo+bestaudio）の場合:**

```
ID.f137.mp4.part   映像ストリーム（ダウンロード中）
ID.f140.m4a.part   音声ストリーム（ダウンロード中）
ID.info.json       メタデータ（ダウンロード開始直後に書き出される）
```

ffmpeg 結合完了後（中間ファイルは yt-dlp が自動削除）:
```
ID.mp4             最終ファイル
ID.info.json
```

**ffmpeg なし（best[ext=mp4]）の場合:**

```
ID.mp4.part        ダウンロード中
ID.info.json
```

完了後:
```
ID.mp4
ID.info.json
```

### ダウンロード完了後の Go 側処理（ファイル名に関わるすべてのステップ）

**Step 1: info.json からタイトルを取得して削除する**

`workDir` 内で `*.info.json` にマッチするファイルを探し、JSON の `title` フィールドを読む。
読み終わったら info.json を削除する（workDir を最終的に空にするため）。
タイトルが取れなかった場合は動画 ID ベースのファイル名のまま次のステップへ進む。

**Step 2: 残りのファイルを outputDir へ移動する**

workDir に残っているファイル（ディレクトリは除く）を全件処理する:

```
拡張子を取得                        例: ".mp4"
最終ファイル名を組み立てる           例: sanitizeFilename(title) + ".mp4"
                                         = "Never Gonna Give You Up.mp4"
uniqueDest で移動先を決める
  outputDir/Never Gonna Give You Up.mp4 が存在しない → そのまま使う
  存在する → "Never Gonna Give You Up (1).mp4" を試す（以降 (2), (3)…）
os.Rename(workDir/ID.mp4, 移動先)
```

**uniqueDest について:**
同名ファイルが存在する場合に連番を付与して保護するための関数。
**絶対に削除・迂回してはならない。** ユーザーの既存ファイルを上書きすることは厳禁。

`uniqueDest` は単に空きパスを返すのではなく、`O_CREATE|O_EXCL` で 0 バイトの
プレースホルダーを作って名前を**アトミックに予約**する。`os.Stat` で空きを確認してから
`os.Rename` するまでの TOCTOU 窓（並行ダウンロード最大 10 で同じタイトルを同時取得した際に
同名を選ぶ競合や、外部プロセスの割り込み）を閉じるため。呼び出し側（runDownload STEP5）は
返ったパスへ実体を `os.Rename` する（`os.Rename` は Unix/Windows ともこのプレースホルダーを
置換する）。**rename に失敗した場合は `os.Remove(dst)` で予約プレースホルダーを後始末する**こと
（0 バイトファイルの残留を防ぐ）。挙動は `TestUniqueDest` / `TestUniqueDestReservesAtomically` で固定。

**sanitizeFilename について:**
処理順は ①**制御文字（C0 制御 `< 0x20` と DEL `0x7f`）を除去** → ②Windows で使えない文字
（`\ / : * ? " < > |`）をアンダースコアに置換 → ③**前後の空白**と**末尾のドット**を除去（先頭の
ドットは保持）→ ④**Windows 予約デバイス名**（`CON` `PRN` `AUX` `NUL` と `COM1`〜`9` / `LPT1`〜`9`、
大小無視・拡張子付きも対象）なら先頭に `_` を付けて回避。制御文字と予約名はリモートタイトル由来の
混入でファイル作成失敗・ログ汚染を起こすため弾く。判定は `isWindowsReservedName` に分離。挙動は
`TestSanitizeFilename` で固定。

#### `(1)` サフィックス問題の根本原因と対策

**根本原因**: `%(title)s.%(ext)s` をそのまま `-o` に渡すと、yt-dlp が出力先に同名ファイルを見つけたとき（CWD が保存先フォルダの場合など）に、自分でファイル名に ` (1)` を付与する。これはメタデータの `title` フィールドではなく **ファイルシステム上の衝突回避** として行われる。

**対策**: `-o` にランダム文字列（起動ごとに `crypto/rand` で生成）を使い、いかなる既存ファイルとも衝突しない一時ファイル名で作業させる。タイトルは `--write-info-json` で別途取得し、Go 側でリネームする。

**やってはいけないこと（過去に繰り返した失敗）:**

| 実装 | 問題 |
|---|---|
| `-o "%(title)s.%(ext)s"` | outputDir に同名ファイルがあると yt-dlp が `(1)` を付ける |
| `-P home:outputDir` | yt-dlp が outputDir を直接チェックするため既存ファイルと競合すると `(1)` が付く |
| `uniqueDest` を削除して直接 Rename | ユーザーの既存ファイルを黙って上書き・破壊する |
| `--print "%(title)s"` でタイトル取得 | 出力テンプレート評価を経るため、yt-dlp が内部で同一 ID を 2 回処理すると `title` に ` (1)` が付加される |

**注意事項**:
- タイトル取得には `--skip-download --dump-json --no-playlist` を使い、返ってきた JSON の `id` と `title` を見る。`id` が `-1` で終わり `title` が ` (1)` で終わる場合は yt-dlp の内部 dedup アーティファクトなので ` (1)` を除去する。
- `%(id)s` の評価値はサイトのエクストラクターによって異なる（例: generic エクストラクターでは URL スラグ + フォーマットインデックスになる場合がある）。
- ページタイトル自体に ` (1)` が含まれる場合は `id` が `-1` で終わらないため誤除去は起きない。
- `-P home:workDir` で最終ファイルが必ず workDir に書かれるかは yt-dlp バージョンや動画サイトによって不安定な可能性がある。より確実な代替は `-o filepath.Join(workDir, "%(id)s.%(ext)s")` で絶対パスを直接指定すること（その場合 `-P home:` は不要）。

### Windows でコンソールウィンドウが一瞬開く問題

**原因**: `exec.Command` で yt-dlp などの外部コマンドを起動すると、Windows は既定でコンソールウィンドウを生成する。

**対策**: `sysproc_windows.go` で `applyOSProcAttr` を定義し、`cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}` をセットする。**`exec.Command` を呼ぶすべての箇所**（STEP2a の `titleCmd` も含む）に `applyOSProcAttr(cmd)` を呼ぶこと。1 箇所でも漏れるとその呼び出し時にウィンドウが出る。

### 後処理

- ダウンロード完了・失敗・キャンセル問わず、作業ディレクトリを `os.RemoveAll` で削除する
- アプリ起動時に前回クラッシュ時の残留作業ディレクトリを掃除する

---

## 進捗通知

- yt-dlp の stdout/stderr を `os.Pipe` で読み取り、出力行をパースする
- パース結果（進捗率・速度・ETA 等）を Wails の `EventsEmit` でフロントエンドへ通知する

---

## リリース

GitHub Actions で `v*` タグ push をトリガーに自動ビルドする。

| ジョブ | ランナー | 成果物 |
|---|---|---|
| build-macos | macos-latest | moviedl-macos.zip |
| build-windows | windows-latest | moviedl-windows.zip |

ffmpeg はバイナリに同梱しない（Windows も含む）。両プラットフォームとも `wails build` をそのまま実行する。Windows の ffmpeg はアプリ初回起動後に `InstallFfmpeg` で取得する（上記「ffmpeg」節を参照）。

GitHub Release の作成は **`gh release create`（gh CLI、ランナー同梱）** で行い、第三者アクションを使わない。`GH_TOKEN: ${{ github.token }}` で認証し、release ジョブは checkout しないため `--repo "$GITHUB_REPOSITORY"` を明示する。タグ名は `env:` 経由で渡す（シェル展開の安全）。

**サードパーティ GitHub Actions は commit SHA でピンする。** 第三者アクションを使う場合は `contents: write` 等の権限下でタグ書き換えによるサプライチェーン攻撃に晒されるため、`@<40桁SHA> # vN` の形でピンする。公式 `actions/*` は任意。
（経緯: 2026-06-10 監査 T8 で `softprops/action-gh-release@v2` を SHA ピン化したが、同アクションが Node 20 ランタイム非推奨（2026-06-16 強制移行）に該当したため、後に `gh release create` へ置換し第三者アクション依存を解消した。）

---

## 並行アクセスとロック規約

`DownloadItem` は複数 goroutine から触られる。`runDownload`（各ダウンロード専用 goroutine）と、
UI 由来の `PauseDownload` / `ResumeDownload` / `CancelDownload` / `StartDownload` などが同時に動きうる。

- **`item.cmd` は `a.mu` の保護下で読み書きする。** `runDownload` はロック内で `item.cmd` を書く。
  Pause/Resume/Cancel 側も**ロック内でローカル変数に退避してから**、ロック外で
  `suspendProcess` / `resumeProcess` / `Kill` を呼ぶ。ロック外で `item.cmd` を直接読むと
  データ競合になる（過去そうだった）。`item.Status` の判定も同様にロック内で退避した値を使う。
- 回帰防止のため **`make test` は `go test -race`** で回す（CI の `make check` も同じ）。
  並行経路を変更したら -race が緑であることを完了条件にする。
- 既知の限界: ダウンロード中の進捗フィールド（`Percent` / `Speed` 等）は `runDownload` の
  scanner ループが直接更新し `emit` で読む経路が残る。現状テストは並行ダウンロードを再現しないため
  -race では顕在化しない。さらに厳密化するなら進捗更新も `a.mu` 配下に寄せる。（2026-06-10 監査 T7）

---

## WebView / IPC セキュリティ

- **WebView にリモートコンテンツを読み込まない。** フロントエンドは `embed` した `frontend/` の
  ローカル資産のみを読む（`main.go` の `assetserver`）。WebView には `App` のメソッド（`AddToQueue`
  など**ファイル書き込みを伴う操作**）がバインドされているため、リモート HTML を読み込むと
  それらが攻撃面になる。外部 URL を WebView に流す導線を足さないこと。
- **DOM へ差し込むリモート由来データ（タイトル・URL・エラー文言・duration 等）は必ず `esc()` を通す。**
  `esc()` は `& < > " '` を実体参照化する。シングルクォートも escape するのは、`onclick="fn('${esc(x)}')"`
  のように**属性内のシングルクォート文字列**へ動的値を埋めるパターンでも XSS にならないようにするため。
  動的に組む属性値・ハンドラ引数には素の値を絶対に入れない。（2026-06-10 監査 T5）
