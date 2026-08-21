# Code Generation Plan — yt-dlp の手動更新

**ユニット**: moviedl-app（単一ユニット）
**手法**: Kent Beck の TDD（Red → Green → Refactor）。例示ベーステスト + PBT を併存（PBT-10）。

## サイクル 1 — 停止計画・影響件数・バージョン解析・登録簿

- [x] **Red**: `helpers_test.go` に `TestPlanStop` / `TestComputeUpdateImpact` / `TestParseYtDlpVersion` / `TestProcRegistry` を追加
  - 実際の失敗: `undefined: planStop` ほか（未実装によるコンパイル失敗）
- [x] **Green**:
  - `App.procs map[*exec.Cmd]struct{}` と `App.updating` を追加。`registerProc` / `unregisterProc` / `liveProcCount` / `beginUpdate` / `endUpdate`
  - `stopTarget` / `planStop`（`item.cmd` をロック内で退避、`paused` は `needsResume`）
  - `UpdateImpact` / `Affected()` / `computeUpdateImpact`（`OtherProcs` は 0 で下限を切る）
  - `parseYtDlpVersion`（1 行目のみ・trim）

## サイクル 2 — 更新オーケストレーション

- [x] `InstallYtDlp` を `downloadYtDlpVerified`（取得 + SHA256 照合、最終パスには書かない）と `placeYtDlp`（chmod + 原子的 rename）に分解。`InstallYtDlp` は両者の合成として振る舞いを保つ
- [x] `UpdateYtDlp` を実装。**順序が要件**: 取得・検証 → `beginUpdate` → 停止 → drain 待ち → 配置 → 再キュー
- [x] `stopTargets`（`needsResume` なら resume してから Kill）/ `killRegisteredProcs`（取得中プロセスの backstop）/ `waitProcsDrained`（10 秒上限）/ `requeueStopped`
- [x] `GetYtDlpVersion` / `YtDlpUpdateImpact` を Wails 公開メソッドとして追加
- [x] drain がタイムアウトした場合は配置せずエラーを返し、停止したアイテムは再キューする

## サイクル 3 — 全経路を登録簿に通す

- [x] `FetchPlaylist`: 登録簿を通す。更新中は起動せずエラー「yt-dlp を更新中です」
- [x] `runDownload` STEP2a のタイトル取得: 登録簿を通す。更新中は事前取得を諦める（タイトルは STEP4 で補える）。短命なので `defer` せず即座に解除して生存数を正確に保つ
- [x] `runDownload` 本ダウンロード: 登録簿を通す。更新中は待ちキューへ戻して return（エラーにしない）
- [x] `GetYtDlpVersion`: 登録簿を通す

## サイクル 4 — 停止されたアイテムが消えるバグの予防

- [x] **Red**: `TestShouldRemoveWhenDone`（`undefined: shouldRemoveWhenDone`）と `TestResetForRequeue`
- [x] **Green**: `shouldRemoveWhenDone` を切り出し `"error"` と `"queued"` を残す。`DownloadItem.stopFlag` を追加し `cmd.Wait()` のエラー経路で `"error"` ではなく `"queued"` にする
- [x] **Refactor**: `requeueStopped` から状態遷移を `resetForRequeue` として切り出し、`emit`（Wails ctx 必須）に触れずに単体テストできるようにした

## サイクル 5 — PBT

- [x] `pbt_test.go` に 9 プロパティを追加（planStop 4 / computeUpdateImpact 2 / resetForRequeue 2 / 登録簿 1）
- [x] 既存の `genItems` / `genStatus` ジェネレータを再利用（PBT-07 の再利用性）

## サイクル 6 — フロントエンド

- [x] `yt-dlp: <version>` 行と「更新」ボタンを追加（インストール状態に関わらず常時操作可能）
- [x] `updateYtDlp()`: `YtDlpUpdateImpact` を見て**影響がある場合のみ** `confirm`。件数と「最初からやり直しになる」ことを明示
- [x] 起動時と更新・インストール完了後にバージョン表示を更新
- [x] バインディング再生成（`GetYtDlpVersion` / `YtDlpUpdateImpact` / `UpdateYtDlp` の追加）

## 検証

- [x] `gofmt -l .` — 差分なし
- [x] `go vet ./...` — 警告なし
- [x] `staticcheck ./...` — 警告なし
- [x] `go test -race ./...` — 成功
- [x] PBT 16 件すべて通過（既存 7 + 新規 9）

## 手動確認が必要な項目

自動テストで担保できない箇所（外部プロセス・ネットワーク・UI）。

- [ ] バージョンが起動時に表示される / 未インストールなら「未インストール」
- [ ] 何も実行していない状態で「更新」→ 確認なしで更新され、バージョン表示が変わる
- [ ] ダウンロード実行中に「更新」→ 件数入りの確認が出る。続行すると停止され、更新後に待ちキューから再開される
- [ ] 一時停止中のアイテムがある状態で「更新」→ そのアイテムも停止・再キューされる（サスペンド中でも Kill できている）
- [ ] 停止されたアイテムがリストから消えない
- [ ] 更新中に URL を登録 → 「yt-dlp を更新中です」と表示される
- [ ] ネットワークを切って「更新」→ エラーになるが**実行中のダウンロードは止まらない**（取得失敗が停止より前に起きる）
- [ ] **Windows**: 実行中プロセスを停止したうえで `.exe` の置き換えが成功する
