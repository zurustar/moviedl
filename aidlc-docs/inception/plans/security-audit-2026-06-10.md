# セキュリティ監査 課題リスト（2026-06-10）

リポジトリ全体のセキュリティ監査で検出した課題と修正タスク。重要度順。
各タスクは着手時に CLAUDE.md のワークフロー（TDD・デグレ防止）に従う。

関連ドキュメント:
- [design.md](../application-design/design.md)（ピットフォール・設計取り決め）
- [requirements.md](../requirements/requirements.md)

凡例: `[ ]` 未着手 / `[x]` 完了

---

## 要対応（Medium）

### [x] T1. Go 標準ライブラリの既知脆弱性 2 件を解消（リリースに直結） ✅ 2026-06-10 完了

govulncheck で 2 件の到達可能な脆弱性を検出。いずれも信頼できないユーザー入力 URL の処理経路に関わる。

- **GO-2026-4601**: `net/url` の IPv6 ホストリテラル不正パース（go1.25.8 で修正）。到達経路 = `isValidURL` → `url.Parse`（[app.go:600](../../../app.go#L600)）。URL 検証バイパスのリスク。
- **GO-2026-5037**: `crypto/x509` のホスト名照合の非効率パース（go1.25.11 で修正）。Wails 経由で到達。

問題の構造: [go.mod](../../../go.mod) が `go 1.25.0` 固定で、CI / Release が `go-version-file: go.mod` を使うため、リリースバイナリが古いパッチでビルドされ続ける。

**対応**: `go.mod` に `toolchain go1.25.11`（以上）を追加、または go ディレクティブを引き上げる。修正後 `govulncheck ./...` が標準ライブラリ由来 0 件になることを確認する。

**実施内容（2026-06-10）**: `go mod edit -toolchain=go1.25.11` で `toolchain go1.25.11` を追加（`go 1.25.0` の最小言語バージョンは互換性のため維持）。`GOTOOLCHAIN=auto` 環境で go1.25.11 が自動取得・使用されることを確認。CI / Release は `go-version-file: go.mod` で `toolchain` ディレクティブを読むため、リリースバイナリも 1.25.11 でビルドされる。

検証結果:
- `govulncheck ./...` → **No vulnerabilities found**（標準ライブラリ由来 2 件が解消）
- `make check`（fmt + vet + staticcheck + test）→ 全通過
- 変更は go.mod の 2 行のみ（go.sum・他ファイルに影響なし）

### [x] T2. cleanupLeftoverWorkDirs の os.RemoveAll にプレフィックスガードを追加 ✅ 2026-06-10 完了

起動時に `workdirs.json`（0644）に書かれたパスを無検証で再帰削除する（[app.go:1069-1075](../../../app.go#L1069-L1075)）。改竄やバグで不正パスが登録されると任意ディレクトリが消える。「ユーザーファイルの削除・上書き厳禁」ルールに対する防御層が薄い。

**対応**: 削除前に `filepath.Base(d)` が `.moviedl-work-` プレフィックスを持つことを検証してから `os.RemoveAll` する。検証ロジックを純粋関数に切り出し TDD で Red→Green→Refactor。design.md に「workDir 削除はプレフィックス検証必須」を追記。

**実施内容（2026-06-10）**: 定数 `workDirPrefix`（`.moviedl-work-`）と純粋関数 `isManagedWorkDir` を追加。`cleanupLeftoverWorkDirs` はこのガードを通過したパスのみ `os.RemoveAll`。`os.MkdirTemp` のリテラルも定数化。`TestIsManagedWorkDir`（7 ケース）を Red→Green で追加。design.md「workDir 削除はプレフィックス検証必須」追記済み。

---

## 設計上の残存リスク（Low〜情報提供）

### [x] T3. yt-dlp / ffmpeg 取得の真正性（authenticity）に関する残存リスクを文書化 ✅ 2026-06-10 完了

チェックサムをバイナリと同じ GitHub リリースから取得するため、転送破損は検出できるがリリース侵害には無力（[app.go:436](../../../app.go#L436), [app.go:310](../../../app.go#L310)）。`releases/latest` 参照でバージョン固定なし。インストール後のバイナリ（ユーザー書き込み可能ディレクトリ）は起動時に再検証されない。

**対応**: まず design.md に残存リスクとして明記（必須）。任意で（a）バージョンピン + ピン時点のダイジェスト同梱、（b）起動時の実行ファイル再検証、を検討事項として記録。

**実施内容（2026-06-10）**: design.md「インストール時の完全性検証」に「残存リスク（完全性 ≠ 真正性）」を追記。リリース侵害・バージョン非固定・配置後の非再検証を明記し、強化策（a）（b）を検討事項として記録。コード変更なし（仕様トレードオフとして許容）。

### [x] T4. uniqueDest の TOCTOU による上書きを防ぐ ✅ 2026-06-10 完了

`os.Stat` で空き確認 → `os.Rename` の間に同名ファイルが作られると無警告で上書き（[app.go:1083-1096](../../../app.go#L1083-L1096)）。並行ダウンロード（最大 10）で同タイトル同時取得時に理論上衝突。「上書き厳禁」ルールに抵触しうる。

**対応**: `O_CREATE|O_EXCL` での予約、または `os.Link` を用いたアトミックな配置に変更。衝突を再現する失敗テストを書いてから修正（回帰テスト化）。

**実施内容（2026-06-10）**: メモリ「uniqueDest 必須・削除禁止」を尊重し**関数名は維持**したまま、内部を `O_CREATE|O_EXCL` のアトミック予約に強化（シグネチャ不変）。STEP5 の呼び出し側で rename 失敗時に予約プレースホルダーを `os.Remove` で後始末。`TestUniqueDestReservesAtomically`（touch せず 2 回呼んで別パスになること）を Red→Green で追加。既存 `TestUniqueDest` も継続通過。design.md「uniqueDest について」更新済み。

### [x] T5. フロントエンド esc() にシングルクォートエスケープを追加（XSS 予防的硬化）✅ 2026-06-10 完了

`esc()` が `'` を未エスケープ（[index.html:662-666](../../../frontend/index.html#L662-L666)）。現状 `onclick` 内に入るのは数値 ID のみで実害なしだが、将来リモート由来値を同パターンで埋めると即 XSS になる脆い構造。WebView には書き込みを伴う App メソッドがバインドされている点も留意。

**対応**: `esc()` に `'` → `&#39;` を追加。design.md に「リモートコンテンツを WebView に読み込まない」「動的属性値は必ず esc() 経由」を明記。

**実施内容（2026-06-10）**: `esc()` に `.replace(/'/g,'&#39;')` を追加。design.md に「WebView / IPC セキュリティ」節を新設し、ローカル資産のみ読み込む方針・動的属性値の esc() 必須を明記。フロントエンドのため自動テストなし（次回手動確認: 通常 DL・プレイリスト・エラー表示の経路）。

### [x] T6. sanitizeFilename に制御文字・Windows 予約デバイス名の処理を追加 ✅ 2026-06-10 完了

パス区切り・予約文字は処理済みだが、リモートタイトル由来の制御文字（改行等）と Windows 予約名（CON, NUL, COM1 等）が未処理（[app.go:1077-1081](../../../app.go#L1077-L1081)）。パストラバーサルは防御済みで実害は「保存失敗・奇妙な名前」止まり。

**対応**: 制御文字の除去と予約デバイス名のリネームを `sanitizeFilename` に追加。各ケースのテーブルテストを TDD で追加。

**実施内容（2026-06-10）**: `sanitizeFilename` に①制御文字（C0 + DEL）除去、④Windows 予約名（`isWindowsReservedName` で判定し先頭 `_` 付与）を追加。`TestSanitizeFilename` に制御文字・予約名・非予約名（CONTACT/COM0/COM10）の 11 ケースを Red→Green で追加。design.md「sanitizeFilename について」を処理順込みで更新。

### [x] T7. DownloadItem のロック規約を整理（データ競合）✅ 2026-06-10 完了（cmd 競合修正・進捗フィールドは限界として明文化）

`CancelDownload` / `PauseDownload` が `item.cmd` をロック外で読み（[app.go:715](../../../app.go#L715), [app.go:669](../../../app.go#L669)）、`runDownload` がロック内で書く。`item.Status` も複数 goroutine から非同期アクセス。セキュリティより堅牢性の問題だが `go test -race` で検出されうる。

**対応**: cmd / Status のアクセスをロック規約で統一。CI に `go test -race` 追加を検討。

**実施内容（2026-06-10）**: `PauseDownload` / `ResumeDownload` / `CancelDownload` で `item.cmd`（と Cancel の `item.Status` 判定値）を**ロック内でローカルに退避**してからロック外で suspend/resume/kill する形に統一。`make test` を `go test -race` 化（CI の `make check` 経由で回帰検出）。design.md「並行アクセスとロック規約」節を新設。**既知の限界**: ダウンロード中の進捗フィールド（Percent/Speed 等）は scanner ループが直接更新し emit が読む経路が残る（現テストは並行 DL を再現しないため -race 非顕在）。厳密化は進捗更新の `a.mu` 配下化として節に明記。

### [x] T8. GitHub Actions のサードパーティアクションを SHA ピンに ✅ 2026-06-10 完了

権限は最小限で健全だが、`softprops/action-gh-release@v2` 等サードパーティアクションはコミット SHA ピンでサプライチェーン防御を強化できる（`contents: write` を持つため）([release.yml](../../../.github/workflows/release.yml))。

**対応**: サードパーティアクションを SHA ピンに変更（公式 actions/* は任意）。

**実施内容（2026-06-10）**: `softprops/action-gh-release@v2` を `@3bb12739c298aeb8a4eeaf626c5b8d85266b0e65 # v2` に SHA ピン（`gh api` で v2 タグの commit を確認）。公式 `actions/*` は GitHub 所有のため据え置き。design.md「リリース」節にピン方針を追記。

---

## 問題なしと確認した点（再発防止のため記録）

- **引数インジェクション**: `isValidURL` で http/https + Host 必須、全 exec で `--` 区切り、AddToQueue で再検証
- **zip 展開**: エントリ名をパスに使わず単一ファイルを temp 展開 → zip-slip なし
- **チェックサム検証**: 検証前の実体を最終パスに置かない、sums 取得は `io.LimitReader` で 1MB 制限、`os.Rename` で原子的配置
- **HTTP**: タイムアウト設定済み・HTTPS のみ・ステータスコード検証あり
- **既存 XSS 対策**: リモート由来データは全て `esc()` 経由、属性は二重引用符で統一
- **CI 権限**: `contents: read`、`pull_request_target` 不使用
