# AI-DLC State Tracking

## Project Information
- **Project Type**: Brownfield
- **Start Date**: 2026-08-06T00:00:00Z
- **Current Stage**: CONSTRUCTION - Build and Test（完了・承認待ち）

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
| Extension | Enabled | Enforcement Mode | Decided At |
|---|---|---|---|
| Security Baseline | No（今回はスキップ） | - | Requirements Analysis |
| Property-Based Testing | Yes | Full（全 PBT ルールを blocking として強制） | Requirements Analysis |

**Note**: Security Baseline は opt-out のため `security-baseline.md` はロードしない。Property-Based Testing は opt-in のため `property-based-testing.md` をロード済み。Go 用 PBT フレームワークは **`pgregory.net/rapid` v1.3.0 に確定**（PBT-09。`go.mod` に追加済み、`design.md`「テスト可能プロパティ（PBT-01）」に記録）。

## Stage Progress
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
- [x] Code Generation — [code-generation-plan.md](construction/plans/code-generation-plan.md)（TDD 5 サイクル完了）
- [x] Build and Test — [build-and-test-summary.md](construction/build-and-test/build-and-test-summary.md)（`make check` 成功・承認待ち）

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
