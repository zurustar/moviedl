# moviedl 設計書

## アーキテクチャ

Wails v2 を使用する。Go バイナリが WebView ウィンドウを内包し、フロントエンド（HTML/CSS/JS）と Go バックエンドが IPC で通信する。

- フロントエンド: 単一 HTML ファイル（フレームワークなし）
- バックエンド: Go
- IPC: `window.go.main.App.*`（JS → Go）/ `window.runtime.EventsOn`（Go → JS）

---

## 動画ダウンロード

### yt-dlp

動画の取得に yt-dlp を使用する。

- 実行ファイルは `os.UserConfigDir()/moviedl/` に配置する
- 未インストールの場合はアプリ内からダウンロードできる

### ffmpeg

映像・音声ストリームの結合およびリマックスに ffmpeg を使用する。

- Windows: リリースバイナリに同梱（Go の `embed` で埋め込み、起動時に展開）
- macOS: Homebrew の標準インストールパスを直接参照（macOS GUI アプリはシェルの PATH を継承しない）

### フォーマット選択

ffmpeg が利用可能な場合:

```
-f bestvideo+bestaudio/best --merge-output-format mp4
```

ffmpeg がない場合:

```
-f best[ext=mp4]/best
```

---

## ダウンロード状態管理

### 単一リストモデル

`App` 構造体は `items []*DownloadItem` の単一リストでアイテムを管理する（順序＝表示順序）。  
各アイテムの状態は `Status` フィールドで表す。チャネルベースの単一ワーカーは廃止する。

```go
type App struct {
    ctx     context.Context
    mu      sync.Mutex
    items   []*DownloadItem  // 登録順に並ぶ単一リスト
    schedCh chan struct{}     // 状態変化通知用（バッファ 1）
}
```

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
items の中に Status == "downloading" のものが存在しない
かつ
items の中に Status == "queued" のものが存在する
  → 最初の "queued" アイテムを "downloading" に変更し goroutine を起動
```

`"downloading"` が 1 件でもあれば何もしない（手動開始ではない限り追加起動しない）。

### 手動開始（StartDownload）

`StartDownload(id string)` を JS から呼ぶと:

1. `items` の中から該当アイテムを探す（Status が `"queued"` または `"paused"` であること）
2. Status を `"downloading"` に変更し、ダウンロード goroutine を起動する
3. `schedCh` に通知（scheduler は `"downloading"` が存在するため何もしない）

### ダウンロード完了時

`runDownload` goroutine が終了するとき（成功・失敗・キャンセル問わず）:

1. Status を最終状態（`"finished"` / `"error"` / `"cancelled"`）に更新する
2. `schedCh` に通知する（`"downloading"` が 0 件になった場合、scheduler が次を自動起動）

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

1. ユーザーが URL を入力して「情報取得」を実行する
2. Go 側で `yt-dlp --flat-playlist --dump-json --no-download URL` を実行する
3. 複数エントリが含まれる場合、エントリ一覧（ID・タイトル・サムネイル等）を JS に返す
4. フロントエンドが選択 UI を表示し、ユーザーがダウンロードしたいエントリを選ぶ
5. 「追加」を押すと選択されたエントリが個別の `DownloadItem` として `items` の末尾に追加される

単一動画 URL の場合は従来どおり直接 `items` に追加する。

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
- 保存先フォルダ内に置く理由:
  - クラッシュ時にユーザーが可視・手動削除できる
  - 保存先と同一ファイルシステムのため、ファイル移動が確実

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

**sanitizeFilename について:**
Windows で使えない文字（`\ / : * ? " < > |`）をアンダースコアに置換し、末尾のスペース・ドットを除去する。

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

Windows ビルド時に ffmpeg バイナリを `embedded/` へ配置してから `wails build` を実行し、ffmpeg を同梱したシングルバイナリを生成する。
