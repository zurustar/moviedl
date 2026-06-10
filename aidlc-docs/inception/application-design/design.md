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
    maxActive int               // 自動補充で維持する実行中アイテム数の上限（1〜10、既定 1）
}
```

`maxActive` は `mu` で保護する。`SetMaxConcurrent(n int)` で更新（1〜10 にクランプ）したのち `notify()` で scheduler を起こす。`GetMaxConcurrent() int` でフロントエンドの初期値を返す。

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

実装は `sysproc_windows.go` / `sysproc_other.go` に分けて定義する:

```go
// sysproc_other.go
func suspendProcess(cmd *exec.Cmd) error { return cmd.Process.Signal(syscall.SIGSTOP) }
func resumeProcess(cmd *exec.Cmd) error  { return cmd.Process.Signal(syscall.SIGCONT) }

// sysproc_windows.go
func suspendProcess(cmd *exec.Cmd) error { /* NtSuspendProcess */ }
func resumeProcess(cmd *exec.Cmd) error  { /* NtResumeProcess  */ }
```

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

- URL 入力欄を `drop` ターゲットにする
- `dragover` イベントで `preventDefault()` を呼び、入力欄を有効なドロップターゲットとして登録する
- `drop` イベントで `event.preventDefault()` を呼び、ブラウザのデフォルト動作（URL でのページ遷移など）を抑止する
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

**サードパーティ GitHub Actions は commit SHA でピンする。** `softprops/action-gh-release` は
`contents: write` 権限を持つため、タグ（`@v2`）参照ではタグ書き換えによるサプライチェーン攻撃に
晒される。`@<40桁SHA> # v2` の形でピンし、タグ更新時は SHA も併せて見直す。公式 `actions/*` は任意。
（2026-06-10 セキュリティ監査 T8）

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
