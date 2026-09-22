# AI-DLC State Tracking

## Project Information
- **Project Type**: Brownfield
- **Start Date**: 2026-08-06T00:00:00Z
- **Current Stage**: CONSTRUCTION - Build and Test 完了（要件 3「m3u8（HLS）対応」・**実機の手動確認待ち**。自動検証は `make check` 緑・67 テスト通過）
- **完了済み要件**: 要件 1「同時ダウンロード数 0（登録のみモード）」→ v0.2.1 としてリリース済み（commit 35c8767）／要件 2「yt-dlp の手動更新」→ commit 588e13f
- **要件 2 の成果物**: [code-generation-plan-ytdlp-update.md](construction/plans/code-generation-plan-ytdlp-update.md)（TDD 6 サイクル・手動確認チェックリスト付き）
- **要件 3 の現況**: 実現可能性評価を完了 → [m3u8-feasibility.md](inception/requirements/m3u8-feasibility.md)。判定は「実行可能」だが 3 層に分かれる。スコープは**インターネット上の m3u8 URL に確定**（ローカル .m3u8 は対象外・ユーザー回答 2026-09-21）。[requirement-verification-questions.md](inception/requirements/requirement-verification-questions.md) の Q1〜Q6 への回答待ち（Step 6 GATE）。要件 1 の Q&A は [requirement-verification-questions-archive.md](inception/requirements/requirement-verification-questions-archive.md) へ退避
- **⚠️ 要件 3 の評価中に発見した既存バグ（m3u8 非依存）**: yt-dlp が起動する **ffmpeg 孫プロセスがどの停止経路でも停止されない**。`applyOSProcAttr` が非 Windows で no-op でプロセスグループを作らないため、yt-dlp への SIGKILL で ffmpeg が孤児化して取得を続ける（実測確認）。影響: `CancelDownload` / `PauseDownload` / `UpdateYtDlp` の `waitProcsDrained`。独立要件として立てる候補（Q1 の選択肢 B/C）

## Workspace State
- **Existing Code**: Yes
- **Programming Languages**: Go 1.x（`app.go` / `main.go` / `sysproc_*.go`）、JavaScript + HTML（`frontend/index.html`）
- **Build System**: Wails v2（`wails.json`）+ Go modules + Makefile
- **Project Structure**: 単一デスクトップアプリ（Monolith）
- **Workspace Root**: /Users/oumi/Documents/GitHub/moviedl
- **Reverse Engineering Needed**: No — ユーザー判断によりスキップ（Q4）。既存の `requirements.md` / `application-design/design.md` を Reverse Engineering 成果物の代替として使用し、今回の要件は既存要件に追記する方針

## Code Location Rules
- **Application Code**: Workspace root (NEVER in aidlc-docs/)
- **Documentation**: aidlc-docs/ only
- **Structure patterns**: See code-generation.md Critical Rules

## Extension Configuration
**要件 3（m3u8 対応・現行）:**

| Extension | Enabled | Enforcement Mode | Decided At |
|---|---|---|---|
| Security Baseline | **Yes** | Full（全 SECURITY ルールを blocking として強制） | Requirements Analysis（要件 3・Q5 = A） |
| Property-Based Testing | Yes | Full（全 PBT ルールを blocking として強制） | Requirements Analysis（要件 3・Q6 = A） |

両方 opt-in のため `security-baseline.md` と `property-based-testing.md` をロード済み。
Security Baseline を要件 2 の「No」から変更した理由: 今回は (a) Referer という新しい文字列を yt-dlp の
コマンドラインに渡す（引数インジェクション面の拡大）、(b) プロセスの停止方法そのものを変更する、の 2 点。

**過去の要件での設定（記録）:**

| 要件 | Security Baseline | Property-Based Testing |
|---|---|---|
| 要件 1（同時ダウンロード数 0） | No | Yes / Full |
| 要件 2（yt-dlp の手動更新） | No | Yes / Full |

Go 用 PBT フレームワークは **`pgregory.net/rapid` v1.3.0 に確定**（PBT-09。`go.mod` に追加済み、`design.md`「テスト可能プロパティ（PBT-01）」に記録）。

## Stage Progress — 要件 3（m3u8（HLS）対応）★現行

### 🔵 INCEPTION PHASE
- [x] Workspace Detection — 既存 aidlc-state.md を検出しレジューム（Brownfield）
- [x] Reverse Engineering — SKIPPED（要件 1 の判断を継続。既存 requirements.md / design.md で代替）
- [x] Requirements Analysis — 実現可能性評価 [m3u8-feasibility.md](inception/requirements/m3u8-feasibility.md) → Q1〜Q6 回答済み → [requirements.md](inception/requirements/requirements.md)「m3u8（HLS）対応」節。**APPROVED**（2026-09-22「作業を続けて」）
- [x] User Stories — SKIPPED（新しいペルソナもユーザーワークフローも生じない。可視の変更は保存名の規則・警告文・エラー文言の 3 点で既存フロー内の表示変更のみ。バグ修正部分は再現手順が実測で確定）
- [x] Workflow Planning — [workflow-plan-m3u8.md](inception/plans/workflow-plan-m3u8.md)（Mermaid 構文検証済み・テキスト代替あり）。**承認待ち**
- [x] Application Design（standard）— design.md に「m3u8 / HLS」節と「プロセス管理（停止は孫プロセスまで及ばせる）」節を追加。既存「停止対象は『実行中』だけでは足りない」節の取りこぼし経路を 3 → **4**（孫プロセス）に訂正。PBT-01 プロパティ 13 件を記載
- [x] Units Generation — SKIPPED（単一ユニット）

### 🟢 CONSTRUCTION PHASE（ユニット: moviedl-app）
- [x] Functional Design — SKIPPED（Application Design に統合）
- [x] NFR Requirements（PBT-09 のみ・新規判断なし）— `pgregory.net/rapid v1.3.0` を継続
- [x] NFR Design — SKIPPED（新規 NFR パターンなし。セキュリティ要件 5 件は具体的な実装規則のため design.md に直接記載）
- [x] Infrastructure Design — SKIPPED（インフラ構成なし）
- [x] Code Generation — [code-generation-plan-m3u8.md](construction/plans/code-generation-plan-m3u8.md)（TDD 13 サイクル完了・計画からの差異 7 件を同ファイルに記録）
- [x] Build and Test — [build-and-test-summary-m3u8.md](construction/build-and-test/build-and-test-summary-m3u8.md)（`make check` 成功・**67 テスト通過**・SECURITY / PBT ともに blocking findings なし）

### ⚠️ 要件 3 の未完了事項
- [ ] **macOS 実機の手動確認**（m3u8 のダウンロード・保存名・キャンセルで ffmpeg が残らないこと・ログのマスク・既存サイトのデグレ確認）
- [ ] **Windows 実機の手動確認** — Job Object の動作は開発機（macOS）で検証不可能。確認できたのはクロスビルドと `go vet` のみ。**「孫プロセスまで停止が及ぶ」という主目的が Windows で達成されているかは未検証**
- [ ] コミット未実施（ユーザーの指示があるまで行わない方針）。変更は論理的に 2 群（m3u8 実用化 / 孤児化修正）に分離可能な状態で作業ツリーにある

### ⚠️ 要件 3 のリスク（Medium〜High）
リスクは**プロセス停止方法の変更**に集中する。(1) 過去に罠を踏んだ `CancelDownload` / `PauseDownload` /
`UpdateYtDlp` に触る、(2) **Windows 実装を開発機（macOS）で検証できない**、(3) 停止範囲を広げるため
「殺しすぎ」の危険がある。低減策として m3u8 の機能追加（低リスク）と停止の修正（高リスク）を
**別コミットに分離し、停止の修正を最後に単独で行う**。

### 確定したスコープ（Q1 = B）
1. 保存ファイル名の自動生成（Q2 = B: ホスト名 + パス要素 + 日時、URL だけから決まる純粋関数）
2. Referer の付与（Q3 = B: m3u8 URL のオリジンを送る。**適用はパスが `.m3u8` で終わる URL に限定**）
3. 期限切れ URL の案内（Q4 = D: 登録時の警告 + 失敗時の説明）
4. **子プロセス（ffmpeg）が停止されない既存不具合の修正** — キャンセル・一時停止・yt-dlp 更新の排出判定の 3 経路
5. セキュリティ要件（Q5 = Yes による）— Referer 値の検証、ログのトークンマスク、停止対象の限定

**対象外:** ライブ配信の録画、ローカル .m3u8 の読み込み、DRM、元ページ URL のユーザー指定

---

## Stage Progress — 要件 1・2（完了・記録）

### 🔵 INCEPTION PHASE
- [x] Workspace Detection
- [x] Reverse Engineering — SKIPPED（ユーザー判断、既存ドキュメントで代替）
- [x] Requirements Analysis — APPROVED
- [x] User Stories — SKIPPED（既存 UI の選択肢に 1 値追加するだけで新しいユーザーワークフローが生じない）
- [x] Workflow Planning — [workflow-plan.md](inception/plans/workflow-plan.md)
- [x] Application Design（minimal）— design.md に「maxActive == 0（登録のみモード）」「テスト可能プロパティ（PBT-01）」を追加
- [x] Units Generation — SKIPPED（単一ユニット）

### 🟢 CONSTRUCTION PHASE（ユニット: moviedl-app）
- [x] Functional Design — Application Design に統合（新規ビジネスロジックなし）
- [x] NFR Requirements（PBT-09 のみ）— `pgregory.net/rapid` を採用
- [x] NFR Design — SKIPPED
- [x] Infrastructure Design — SKIPPED
- [x] Code Generation — [code-generation-plan.md](construction/plans/code-generation-plan.md)（TDD 5 サイクル完了）／要件 2 は [code-generation-plan-ytdlp-update.md](construction/plans/code-generation-plan-ytdlp-update.md)（TDD 6 サイクル完了）
- [x] Build and Test — [build-and-test-summary.md](construction/build-and-test/build-and-test-summary.md)（`make check` 成功）

## PBT Compliance（Code Generation / Build and Test）

| ルール | 判定 | 根拠 |
|---|---|---|
| PBT-01 プロパティ特定 | Compliant | `design.md`「テスト可能プロパティ（PBT-01）」に 7 プロパティを categoryed 記載し、code-generation-plan.md から参照 |
| PBT-02 ラウンドトリップ | N/A | 今回の変更対象に逆関数を持つ操作（直列化・符号化・パース）はない |
| PBT-03 不変条件 | Compliant | 範囲制約（0〜10）・要素保存・順序保存・副作用なしを PBT で検証 |
| PBT-04 冪等性 | Compliant | `TestPropSetMaxConcurrentIdempotent` |
| PBT-05 Oracle | Compliant | `TestPropSelectToStartCountMatchesOracle`（件数の参照計算と比較） |
| PBT-06 ステートフル PBT | N/A | 今回変更した状態は単一 int（`maxActive`）で、コマンド列に依存する遷移を持たない。`items` の遷移はダウンロード実行（外部プロセス）と不可分で単体では回せない |
| PBT-07 ジェネレータ品質 | Compliant | `genStatus` は実在 `Status` のみ、`genItems` は構造的に妥当なスライス、`genAnyConcurrency` は境界外・極値を含む。生プリミティブのみのジェネレータは使用していない |
| PBT-08 shrink と再現性 | Compliant | rapid のデフォルト shrink を無効化していない。失敗時に `-rapid.seed=N` が出力されることをミューテーションで実証。CI は `RAPID_SEED=${{ github.run_id }}` を出力（`.github/workflows/ci.yml`） |
| PBT-09 フレームワーク選定 | Compliant | `pgregory.net/rapid v1.3.0`（`go.mod`）。カスタムジェネレータ・shrink・シード再現・`go test` 統合をすべて満たす |
| PBT-10 例示ベースとの併存 | Compliant | `pbt_test.go` を分離。0 の保持・既定 0・0 で自動補充しない・0 で実行中を止めないの各シナリオに例示ベーステストが存在 |

**Blocking PBT findings: なし**
