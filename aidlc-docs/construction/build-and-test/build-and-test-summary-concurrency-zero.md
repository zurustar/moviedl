# Build and Test — 同時ダウンロード数 0（登録のみモード）

対象は単一ユニット（moviedl-app）のみで、ユニット間結合・インフラ・性能特性に変化がないため、
統合テスト / 性能テストの個別手順書は作成しない（該当なし）。ビルド手順は [CONTRIBUTING.md](../../../CONTRIBUTING.md) が正典。

## ビルド

```sh
make build            # 現在のプラットフォーム向け
make build-windows    # Windows amd64
```

Go 側の変更は `app.go` のクランプ範囲と既定値のみ、フロントエンドは `index.html` のプルダウン生成のみ。
`SetMaxConcurrent` / `GetMaxConcurrent` のシグネチャは不変なので `wailsjs` バインディングの再生成は不要。

## ユニットテスト

```sh
make test             # go test -race ./...
make check            # gofmt + go vet + staticcheck + test（CI と同一）
```

### 例示ベーステスト（PBT-10）

| テスト | 検証内容 |
|---|---|
| `TestSetMaxConcurrent` (`app_test.go`) | 0 保持 / 負値 → 0 / 1 / 10 / 11 → 10 |
| `TestNewAppDefaultsToRegistrationOnly` (`app_test.go`) | 起動時の既定が 0 |
| `TestSelectToStart` (`helpers_test.go`) | 「maxActive が 0 なら何も起動しない」「0 でも実行中は無視される」を含む |
| `TestSetMaxConcurrentZeroDoesNotTouchItems` (`helpers_test.go`) | 0 へ引き下げても実行中・一時停止中の状態を変えない |

### プロパティベーステスト（`pbt_test.go`、`pgregory.net/rapid`）

```sh
go test -run TestProp ./...                       # PBT のみ実行
go test -run TestProp -rapid.checks=1000 ./...    # ケース数を増やす
go test -run TestPropXxx -rapid.seed=N ./...      # 失敗時の再現（N は失敗出力のシード）
```

- **shrink**: rapid のデフォルト動作を使用（無効化していない）。失敗時は最小反例が出力される（PBT-08）
- **シード**: 失敗時に `-rapid.seed=N` と `-rapid.failfile=...` が出力される。`testdata/rapid/` の失敗ファイルは `.gitignore` 済み（ローカル調査用）
- **CI**: `.github/workflows/ci.yml` が `RAPID_SEED=${{ github.run_id }}` を設定し、ログの先頭にシードを出力する。実行ごとにシードは変わるが run ログに残るため失敗を必ず再現できる（PBT-08）
- **flaky 時の対応**: 再実行で通る PBT 失敗は握りつぶさず、出力されたシード / failfile で再現し原因を特定する。修正時は shrink された最小反例を例示ベーステストとして恒久化する（PBT-10）

## 手動確認（フロントエンド）

自動テスト対象外の UI 経路。変更後に以下を実機で確認する。

- [ ] 起動直後、プルダウンが `0（登録のみ）` になっている
- [ ] 0 のまま URL を登録 → 待ちキューに積まれるがダウンロードが始まらない
- [ ] 0 のままプレイリスト URL を登録 → 一覧モーダルが出てファイル選択ができ、選択分が待ちキューに積まれる（ダウンロードは始まらない）
- [ ] 0 のまま待ちキューのアイテムを手動開始 → ダウンロードが始まる
- [ ] 0 のまま一時停止中アイテムを再開 → ダウンロードが再開する
- [ ] 0 → 3 に変更 → 待ちキューの先頭 3 件が自動で開始される
- [ ] 3 件実行中の状態で 0 に変更 → 実行中の 3 件は止まらず完走し、完了後に新しいアイテムは自動開始されない
- [ ] 10 を選択でき、11 以上は選択肢に存在しない

## 実行結果（2026-08-06）

```
$ gofmt -l .
（差分なし）

$ go vet ./...
（警告なし）

$ staticcheck ./...
（警告なし）

$ go test -race ./...
ok  	moviedl	1.614s

$ go test -run TestProp -v ./...
--- PASS: TestPropSetMaxConcurrentStaysInRange        [rapid] OK, passed 100 tests
--- PASS: TestPropSetMaxConcurrentIdentityInRange     [rapid] OK, passed 100 tests
--- PASS: TestPropSetMaxConcurrentIdempotent          [rapid] OK, passed 100 tests
--- PASS: TestPropSelectToStartNeverExceedsLimit      [rapid] OK, passed 100 tests
--- PASS: TestPropSelectToStartCountMatchesOracle     [rapid] OK, passed 100 tests
--- PASS: TestPropSelectToStartReturnsQueuedItemsInOrder  [rapid] OK, passed 100 tests
--- PASS: TestPropSelectToStartHasNoSideEffects       [rapid] OK, passed 100 tests
PASS
ok  	moviedl	0.411s

$ make check
go vet ./...
go run honnef.co/go/tools/cmd/staticcheck@v0.7.0 ./...
go test -race ./...
ok  	moviedl	(cached)
```

---

## PBT コンプライアンス（Property-Based Testing / Full 強制）

| ルール | 判定 | 根拠 |
|---|---|---|
| PBT-01 プロパティ特定 | Compliant | `design.md`「テスト可能プロパティ（PBT-01）」に 7 プロパティを categoryed 記載し、[code-generation-plan-concurrency-zero.md](../plans/code-generation-plan-concurrency-zero.md) から参照 |
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

（この表は当初 aidlc-state.md に記録していたものを、2026-09-23 のドキュメント整理で要件 1 のテスト結果である本ファイルへ移した。内容は変更していない。）
