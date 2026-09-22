# Code Generation Plan — m3u8（HLS）対応

- 要件: [requirements.md](../../inception/requirements/requirements.md)「m3u8（HLS）対応」
- 設計: [design.md](../../inception/application-design/design.md)「m3u8 / HLS」「プロセス管理（停止は孫プロセスまで及ばせる）」
- 実行計画: [workflow-plan-m3u8.md](../../inception/plans/workflow-plan-m3u8.md)
- ユニット: moviedl-app（単一）

## 方式

[CLAUDE.md](../../../CLAUDE.md)「テスト駆動開発」に従い **Red → Green → Refactor を 1 サイクルずつ**回す。
各サイクルで `go test` の赤と緑を確認する。例示ベーステストと PBT を併存させる（PBT-10）。

**コミットを 2 つに分ける**（[workflow-plan-m3u8.md](../../inception/plans/workflow-plan-m3u8.md) のリスク低減策）:

- **コミット A（低リスク・独立）**: m3u8 の実用化。純粋関数の追加と統合のみ。既存の停止処理に触らない
- **コミット B（高リスク・単独）**: プロセスツリー停止。過去に罠を踏んだ経路に触るため単独で行う

---

## コミット A: m3u8 の実用化

### サイクル A1: `isM3U8URL`
- [x] Red: `app_test.go` に `TestIsM3U8URL` を書く。クエリ付き（`...master.m3u8?token=x`）が真になること、大小混在（`.M3U8`）が真になること、`.mp4` や `.m3u8` を含むだけのホスト名が偽になることを表明 → 失敗（未定義）を確認
- [x] Green: `app.go` に `isM3U8URL` を実装（`neturl.Parse` の `Path` に対して大小無視の接尾辞判定）
- [x] Refactor: `go test` 緑を保ったまま整理

### サイクル A2: `refererFor`
- [x] Red: `TestRefererFor` を書く。m3u8 URL に対し `scheme://host/` を返すこと、m3u8 でない URL / 不正 URL に対し `""` を返すこと、ポート付きホストが保たれることを表明 → 赤を確認
- [x] Green: `isM3U8URL` と `isValidURL` を通してからオリジンを組み立てる実装
- [x] Refactor

### サイクル A3: `buildYtDlpArgs` への `--referer` 付与
- [x] Red: 既存 `TestBuildYtDlpArgs` に、m3u8 URL のとき `--referer <origin>` が含まれること、**m3u8 でない URL のとき `--referer` が一切含まれないこと**（デグレ防止の表明）を追加 → 赤を確認
- [x] Green: `buildYtDlpArgs` 内で `refererFor(url)` が非空なら付与する（シグネチャは変更しない）
- [x] Refactor: `--` 終端より前に挿入されていることを確認

### サイクル A4: `m3u8FileName`
- [x] Red: `TestM3U8FileName` を書く。`https://vod.example.com/hls/ab12cd/master.m3u8` → `vod.example.com_ab12cd_<日時>`、汎用語のみのパス（`/hls/master.m3u8`）ではパス要素が省略されること、クエリが結果に含まれないことを表明 → 赤を確認
- [x] Green: ホスト + 末尾から汎用語を飛ばした最初のパス要素 + `yyyymmdd-hhmm` を `_` で連結。拡張子は付けない
- [x] Refactor: 汎用語の集合を定数に切り出す

### サイクル A5: `runDownload` への保存名の統合
- [x] Red: `m3u8FileName` が STEP2a のタイトルを上書きする経路をテストできる形に切り出す（`resolveTitle(url, fetchedTitle, now)` 相当の純粋関数）。`TestResolveTitle` で、m3u8 URL は生成名を使い、m3u8 でない URL は取得タイトルをそのまま使うことを表明 → 赤を確認
- [x] Green: 純粋関数を実装し、`runDownload` の STEP2a 直後から呼ぶ
- [x] Refactor: `logf` に決定根拠を残す（調査のため）

### サイクル A6: `redactLine`
- [x] Red: `TestRedactLine` を書く。クエリ付き URL のクエリが `?<redacted>` になること、**パスは残ること**、URL を含まない行が変化しないこと、二重適用で変化しないことを表明 → 赤を確認
- [x] Green: 正規表現で `https?://[^\s"']+` を拾い、`?` 以降を置換する実装（`neturl.Parse` に依存しない）
- [x] Refactor

### サイクル A7: ログ経路への統合
- [x] Red: `redactLine` を通していないログ経路が残っていないことを、コマンド行組み立ての純粋関数に対するテストで表明 → 赤を確認
- [x] Green: STEP2 のコマンド行と STEP3 の yt-dlp 出力行を `redactLine` 経由にする
- [x] Refactor

### A-PBT: プロパティの追加（`pbt_test.go`）
- [x] URL 専用ジェネレータを実装（スキーム・ホスト・ポート・パス要素・クエリから構造的に妥当な URL を組む。PBT-07）
- [x] `isM3U8URL`: クエリ非依存 / 大小無視
- [x] `refererFor`: **`-` で始まらない** / `isM3U8URL` との対応 / オリジンのみ
- [x] `m3u8FileName`: `sanitizeFilename` 不変 / クエリ非混入 / 非空
- [x] `redactLine`: 冪等 / マスクの網羅 / URL なし行の保存
- [x] 失敗時に `-rapid.seed` が出ることを確認（PBT-08）

### A-検証
- [x] `make check` 緑（`gofmt` / `go vet` / `staticcheck` / `go test -race ./...`）
- [ ] コミット A を作成 ← **未実施**（コミットはユーザーの指示があるまで行わない）

---

## コミット B: プロセスツリー停止（高リスク）

### サイクル B1: `isKillablePID`
- [x] Red: `TestIsKillablePID` を書く。`2` 以上が真、**`1` / `0` / `-1` / 負数が偽**であることを表明 → 赤を確認
- [x] Green: `pid > 1` を実装（OS 非依存ファイルに置き、両 OS のテストで共有する）
- [x] Refactor
- [x] PBT: 任意の `int` に対し真になるのは `pid > 1` のときだけ

### サイクル B2: 非 Windows のツリー抽象
- [x] Red: `applyOSProcAttr` が `Setpgid: true` を設定することを表明するテスト → 赤を確認
- [x] Green: `sysproc_other.go` に `Setpgid: true`、`killProcessTree` / `suspendProcessTree` / `resumeProcessTree` を実装。**符号反転の前に `isKillablePID` でガードする**。`trackProcessTree` / `releaseProcessTree` は no-op
- [x] Refactor
- [x] **実プロセスでの統合テスト**: `sh -c 'sleep 300 & wait'` のような子を産むプロセスを起動し、`killProcessTree` で**子まで死ぬこと**を検証する（孤児化の回帰テスト。macOS/Linux で実行可能）

### サイクル B3: Windows のツリー抽象
- [x] Green: `sysproc_windows.go` に Job Object（`CreateJobObjectW` / `AssignProcessToJobObject` / `TerminateJobObject` / `QueryInformationJobObject`）を実装。`HideWindow: true` は維持
- [x] ビルド検証: `GOOS=windows go build ./...` と `GOOS=windows go vet ./...` が通ること
- [x] ⚠️ **動作検証は Windows 実機の手動確認に委ねる**（開発機 macOS では不可能）

### サイクル B4: 停止経路の切り替え
- [x] Red: `CancelDownload` / `PauseDownload` / `ResumeDownload` がツリー版を呼ぶことを表明 → 赤を確認
- [x] Green: 単一プロセス版の呼び出しをツリー版に置き換える。**ロック内で `item.cmd` を退避してロック外で呼ぶ既存規約を崩さない**。一時停止中は resume してから kill する既存順序も維持
- [x] Refactor
- [x] 回帰確認: `shouldRemoveWhenDone` / `stopFlag` / `resetForRequeue` の既存テストが緑のまま

### サイクル B5: `runDownload` と `waitProcsDrained`
- [x] Green: `cmd.Start()` 直後に `trackProcessTree(cmd)`、`Wait` 後に `releaseProcessTree(cmd)` を追加
- [x] Green: `killRegisteredProcs` をツリー版に切り替える
- [x] 回帰確認: yt-dlp 更新の既存テスト（`planStop` / `computeUpdateImpact` / `resetForRequeue`）が緑のまま

### B-検証
- [x] `make check` 緑
- [x] `GOOS=windows go build ./...` / `GOOS=windows go vet ./...` 通過
- [ ] コミット B を作成 ← **未実施**（同上）

---

## フロントエンド（自動テスト対象外・手動確認）

- [x] 同時ダウンロード数が 0 のときに m3u8 URL を登録したら、期限切れの注意を表示する（**開始は自動化しない**）
- [x] 403 で失敗したアイテムのエラー表示に、URL 失効の可能性と取り直しの案内を含める
- [x] DOM へ差し込む値は既存どおり `esc()` を通す（XSS 対策。design.md「WebView / IPC セキュリティ」）

## 手動確認チェックリスト

### macOS 実機
- [ ] m3u8 URL（VOD）を登録してダウンロードが完了する
- [ ] 保存名が `<host>_<パス要素>_<日時>.mp4` になっている
- [ ] ダウンロード中にキャンセル → `ps` で **ffmpeg / yt-dlp が残っていない**
- [ ] ダウンロード中に一時停止 → 通信が止まる（進捗が進まない）→ 再開で進む
- [ ] `moviedl.log` に**トークン付きクエリが残っていない**
- [ ] 通常サイト（m3u8 以外）のダウンロードが従来どおり動く（デグレ確認）

### Windows 実機
- [ ] 上記 macOS の全項目
- [ ] コンソールウィンドウが開かない（`HideWindow` の維持確認）
- [ ] ダウンロード中に yt-dlp の更新を実行 → 実行ファイルの置き換えが**共有違反にならず成功**する
- [ ] 更新後、停止されたアイテムが待ちキューへ戻っている

## 触ってはいけないもの（デグレ防止）

- `uniqueDest`（既存ファイルの上書き防止）
- `-o` のランダム文字列方式 / `-P home:` `-P temp:` の workDir 集約（`(1)` サフィックス問題の対策）
- `--` 終端 と `isValidURL`（引数インジェクション対策）。**Referer も同じ多層防御を通す**
- `--abort-on-unavailable-fragment`（断片欠損を成功と偽らない）
- `shouldRemoveWhenDone` の `"error"` / `"queued"` 残置
- `maxActive == 0` の一般ルール（**m3u8 のための特別分岐を足さない**）
- `applyOSProcAttr` の `HideWindow: true`（Windows のコンソールウィンドウ抑止）
- `emit` / `notify` のロック規約（ロック保持中に呼ばない）

---

## 実施結果と計画からの差異

`make check` 緑（`gofmt` / `go vet` / `staticcheck` / `go test -race ./...`）、
`GOOS=windows go build` / `go vet` 通過、テスト **67 件** 通過。

計画どおりに進まなかった点を記録する。

### 1. A7 を「各呼び出し側で `redactLine` を呼ぶ」から「ログ行の組み立てでマスクする」に変更

計画は STEP2 / STEP3 の 2 箇所で `redactLine` を呼ぶ想定だった。実装中に、これが
`applyOSProcAttr` や `--` 終端と同じ **「1 箇所でも漏れたらその経路だけ穴が空く」ルールを
新たに 1 つ増やす**ことに気づいたため、`logLine(now, format, v...)` を切り出して
**組み立ての最後に一度だけ** `redactLine` を通す形にした。`logf` の呼び出し側は URL を
含む値をそのまま渡してよく、マスク漏れが構造的に起きない。

### 2. サイクル A8（計画外）を追加: `isYtDlpErrorLine` / `explainDownloadError`

要件「403 で失敗したとき URL 失効の可能性と取り直しを案内する」を満たそうとして、
**`item.Error` には `cmd.Wait()` の err（`"exit status 1"`）しか入っていない**ことが判明した。
yt-dlp が出した `ERROR:` 行は stdout/stderr のスキャナを通ってログに行くだけで、
アイテムには残っていなかった。そのため 403 を判別する材料が存在せず、要件を満たせなかった。

対応として `ERROR:` 行を `runDownload` で捕まえ、`explainDownloadError` で
ユーザー向け文言に変換するサイクルを追加した。戻り値は `redactLine` を通す
（この文言は画面に出て URL コピーでも持ち出されるため。SECURITY-03）。

**副作用（意図した改善）**: m3u8 以外のダウンロードでも、失敗理由が
`"exit status 1"` から yt-dlp の実際のエラー行に変わる。

### 3. `processTreeGone` / `waitTreesGone`（計画では関数として明示していなかった）

要件「更新時のプロセス排出判定は子プロセスの終了も待つこと」を満たすには、
登録簿（yt-dlp 本体しか数えない）だけでは不足だった。OS 別の `processTreeGone` と
共通の `waitTreesGone` を追加し、`UpdateYtDlp` STEP4 で
**登録簿の排出とツリーの消滅の両方**を待つようにした。
あわせて `killRegisteredProcs` が停止を試みたコマンドを返すよう変更した。

### 4. `isKillablePGID` → `isKillablePID` に改名し `app.go` へ配置

Windows 側でもジョブから列挙した pid の妥当性確認に使うため、"PGID" という
Unix 固有の名前は不適切だった。OS 非依存の `app.go` に置き、両 OS から使う。
design.md の記述も実装に合わせて更新済み。

### 5. `IsM3U8URL`（Wails バインディング）を追加

フロントエンドの登録時警告に m3u8 判定が必要だが、JS 側で `.m3u8` の文字列判定を書くと
述語が二重化し、クエリ付き URL の扱いで食い違う。Go の `isM3U8URL` に一本化するため
読み取り専用の IPC メソッドを追加した。

### 6. Windows は「失敗したら従来動作にフォールバック」する設計にした

開発機が macOS のため Job Object の動作検証ができない。そこでジョブの作成・割り当てに
失敗した場合は、ツリー停止関数が**従来どおりの単一プロセス停止に落ちる**構造にした。
これにより Windows で以前より悪くなることはない。`SetInformationJobObject`
（`JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`）は構造体レイアウトのミスを検証できないため採用せず、
`TerminateJobObject` の明示呼び出しのみにした。

### 7. コミットは未実施

計画ではコミット A / B を作る想定だったが、コミットはユーザーの明示的な指示があるまで
行わない方針のため作成していない。変更は論理的に 2 群に分離できる状態で作業ツリーにある。
