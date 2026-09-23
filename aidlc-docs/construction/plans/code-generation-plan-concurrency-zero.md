# Code Generation Plan — 同時ダウンロード数 0（登録のみモード）

**ユニット**: moviedl-app（単一ユニット）
**手法**: Kent Beck の TDD（Red → Green → Refactor）を 1 サイクルずつ。例示ベーステスト + PBT を併存（PBT-10）。

## サイクル 1 — SetMaxConcurrent が 0 を保持する / 既定が 0

- [x] **Red**: `app_test.go` に `TestSetMaxConcurrent`（0 保持・負値 0・1・10・11→10）と `TestNewAppDefaultsToRegistrationOnly` を追加し失敗を確認
  - 実際の失敗: `SetMaxConcurrent(0) 後の GetMaxConcurrent() = 1, want 0` / `NewApp() の maxActive = 1, want 0`（4 件失敗）
- [x] **Green**: `app.go` のクランプ下限を `n < 1 → n = 1` から `n < 0 → n = 0` に変更、`NewApp` の `maxActive: 1` を `0` に変更
- [x] **Refactor**: `App.maxActive` フィールドコメントと `SetMaxConcurrent` の doc コメントを 0〜10・登録のみモードの記述に更新

## サイクル 2 — 0 のとき自動補充されない（回帰テスト）

- [x] `helpers_test.go` の `TestSelectToStart` に「maxActive が 0 なら何も起動しない」「0 でも実行中は無視される」を追加
- [x] `helpers_test.go` に `TestSetMaxConcurrentZeroDoesNotTouchItems`（0 へ引き下げても実行中・一時停止中の状態を変えない）を追加
- [x] **結果: 追加時点で緑**。`selectToStart` は `active >= maxActive` の一般ルールにより `maxActive == 0` で即 break するため実装変更は不要だった。`SetMaxConcurrent` も `items` を触らないため同様。**実装は変えず、要件をコードに固定する回帰テストとして残した**（design.md「maxActive == 0（登録のみモード）」の「0 専用の分岐を追加してはいけない」を守るため）

## サイクル 3 — PBT（`pgregory.net/rapid`）

- [x] `pgregory.net/rapid v1.3.0` を依存に追加（PBT-09）
- [x] `pbt_test.go` を新規作成（例示ベーステストとファイル分離 = PBT-10）
- [x] ドメインジェネレータ `genStatus` / `genItems` / `genAnyConcurrency` を定義（PBT-07。`Status` は実在値のみ、境界値を含む）
- [x] プロパティ 7 件を実装（範囲制約 / 恒等 / 冪等 / Oracle / 要素・順序保存 / 副作用なし）
- [x] **ミューテーション確認**: クランプ下限を一時的に 1 に戻すと `TestPropSetMaxConcurrentIdentityInRange` が失敗し `-rapid.seed=...` が出力されることを確認（PBT が空回りしていないことの検証 + PBT-08 の shrink/シード動作確認）。確認後 `app.go` を復元

## サイクル 4 — フロントエンド

- [x] `frontend/index.html` のプルダウン生成を `n = 1` から `n = 0` に変更
- [x] 0 の表示ラベルを `0（登録のみ）` にして意味が伝わるようにする（value は `"0"` のまま）
- [x] `wailsjs` バインディングは `SetMaxConcurrent(n int)` / `GetMaxConcurrent() int` のシグネチャ変更がないため再生成不要

## サイクル 5 — CI / ドキュメント

- [x] `.github/workflows/ci.yml` に `RAPID_SEED: ${{ github.run_id }}` を追加しシードをログに残す（PBT-08）
- [x] `.gitignore` に `testdata/rapid/`（rapid の失敗再現ファイル）を追加
- [x] `design.md` に「maxActive == 0（登録のみモード）」節と「テスト可能プロパティ（PBT-01）」節を追加

## 検証

- [x] `gofmt -l .` — 差分なし
- [x] `go vet ./...` — 警告なし
- [x] `staticcheck ./...` — 警告なし
- [x] `go test -race ./...` — `ok moviedl`（全 PBT が 100 ケース通過）
- [x] `make check` — 成功
