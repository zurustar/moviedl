# Build and Test Summary — m3u8（HLS）対応

- 要件: [requirements.md](../../inception/requirements/requirements.md)「m3u8（HLS）対応」
- 設計: [design.md](../../inception/application-design/design.md)「m3u8 / HLS」「プロセス管理（停止は孫プロセスまで及ばせる）」
- 実装計画と差異: [code-generation-plan-m3u8.md](../plans/code-generation-plan-m3u8.md)

---

## ビルドとテストの実行結果

| コマンド | 結果 |
|---|---|
| `gofmt -l .` | 差分なし |
| `go vet ./...` | 警告なし |
| `staticcheck ./...` | 警告なし |
| `go test -race ./...` | **ok moviedl**（**67 件通過**） |
| `make check`（上記 4 つ） | **成功** |
| `GOOS=windows GOARCH=amd64 go build ./...` | 成功 |
| `GOOS=windows GOARCH=amd64 go vet ./...` | 警告なし |

テストの内訳:

| 種別 | 件数 | ファイル |
|---|---|---|
| 例示ベース | 41 | `app_test.go`(21) / `helpers_test.go`(7) / `sysproc_other_test.go`(13) |
| プロパティベース（PBT） | 26（今回 +10） | `pbt_test.go` |
| 合計 | **67** | |

### PBT の再現手順（PBT-08）

失敗時は rapid が最小反例と `-rapid.seed=N` を出力する。

```sh
go test -run TestPropRefererForNeverStartsWithDash -rapid.seed=N ./...
```

CI は `RAPID_SEED: ${{ github.run_id }}` を環境変数として出力する（`.github/workflows/ci.yml`）。

### 実プロセスによる統合テスト（非 Windows）

`sysproc_other_test.go` は**実際に子プロセスを産むプロセスを起動して**ツリー停止を検証する。
モックではないため、孤児化の回帰をそのまま捕まえられる。

| テスト | 検証内容 |
|---|---|
| `TestApplyOSProcAttrCreatesProcessGroup` | `Setpgid` が設定される（no-op に戻ったら以降が静かに効かなくなる） |
| `TestKillProcessTreeKillsDescendants` | ツリー停止で**子まで死ぬ**（孤児化の回帰テスト） |
| `TestKillingOnlyLeaderLeavesOrphan` | **対照実験**: リーダーだけ Kill すると子が生き残ることを実証し、ツリー停止で回収できることを確認 |
| `TestSuspendAndResumeProcessTreeAffectsDescendants` | 一時停止・再開が**子の状態（`ps` の `T`）に及ぶ** |
| `TestProcessTreeStopRejectsInvalidPID` | pid が `0` / `1` / `-1` のときシグナルを送らずエラーを返す |
| `TestProcessTreeGoneDetectsSurvivingChild` | リーダーが消えても**子が生きている間は「排出完了」と判定しない** |
| `TestWaitTreesGone` | 生存中はタイムアウトし、ツリー停止後に排出完了を検出する |

---

## ⚠️ 未完了: Windows 実機の手動確認

**Windows のプロセスツリー停止（Job Object）は開発機（macOS）で動作検証できていない。**
確認できたのはクロスコンパイルと `go vet` の通過のみ。

低減策として、ジョブの作成・割り当てに失敗した場合は**従来どおりの単一プロセス停止に
フォールバック**する構造にしてある（Windows で以前より悪くなることはない）。
ただし「孫プロセスまで停止が及ぶ」という**今回の主目的が Windows で達成されているかは未検証**である。

手動確認チェックリストは
[code-generation-plan-m3u8.md](../plans/code-generation-plan-m3u8.md)「手動確認チェックリスト」を参照。

---

## SECURITY コンプライアンス（Security Baseline / Full 強制）

| ルール | 判定 | 根拠 |
|---|---|---|
| SECURITY-01 保存時・通信時の暗号化 | 部分 N/A | 通信: yt-dlp / ffmpeg / チェックサムの取得はすべて HTTPS（TLS 検証はデフォルト有効）。保存時: データストア（DB・オブジェクトストレージ）を持たないため N/A。ダウンロード成果物はユーザー指定フォルダ上のファイルで、暗号化はユーザー環境の責務 |
| SECURITY-02 ネットワーク中継のアクセスログ | N/A | ロードバランサ・API Gateway・CDN を持たないローカルデスクトップアプリ |
| SECURITY-03 アプリログに機微情報を出さない | **Compliant（今回の対応）** | `logLine` が組み立ての最後に `redactLine` を通し、**有効期限トークン付き URL のクエリを `?<redacted>` にマスク**する。各呼び出し側に依存しないため漏れが構造的に起きない。`explainDownloadError` の戻り値（画面に出る文言）も同じくマスク済み。中央ログサービスへの集約は desktop アプリのため N/A |
| SECURITY-04 HTTP セキュリティヘッダ | N/A | HTML を提供するエンドポイントを持たない。WebView は `embed` したローカル資産のみを読む（リモートコンテンツを読み込まない方針は design.md に明文化済み） |
| SECURITY-05 入力検証 | **Compliant** | 入口で `isValidURL`（http/https + ホスト必須）。`refererFor` は導出結果を**再度** `isValidURL` に通す多層防御。`m3u8FileName` は `sanitizeFilename` を通した値のみ返す（プロパティで固定）。`exec.Command` は引数スライス渡しでシェルを介さず、URL は必ず `--` 終端の後ろ。SQL/NoSQL は使用しない |
| SECURITY-06 最小権限 | **Compliant（今回の対応）** | `isKillablePID` が `pid > 1` 以外を拒否し、`kill(0, ...)`（自アプリ）や `kill(-1, ...)`（全プロセス）の成立を塞ぐ。停止範囲は当該ダウンロードのプロセスグループ / Job Object に限定。テストとプロパティの両方で固定 |
| SECURITY-07 ネットワーク構成 | N/A | クラウドのネットワーク構成を持たない |
| SECURITY-08 アプリ層のアクセス制御 | N/A | 単一ユーザーのローカルアプリ。認証・マルチテナントのリソース参照がない |
| SECURITY-09 ハードニング・設定ミス防止 | Compliant（注記あり） | 既定認証情報なし、ディレクトリリスティングなし、サンプルアプリなし。エラー文言は yt-dlp のエラー行を `redactLine` 経由で出す。**注記:** yt-dlp のエラー行には作業ディレクトリのパスが含まれうる。ただし本アプリは単一ユーザーのローカルアプリで、表示先はそのパスの所有者本人であるため第三者への露出にはならないと判断した |
| SECURITY-10 サプライチェーン | Compliant（既知の例外あり） | `go.mod` / `go.sum` をコミット済み。yt-dlp / ffmpeg は SHA256 照合後に配置。staticcheck はバージョン固定（`@v0.7.0`）。第三者 GitHub Actions は使用しない（`gh release create`）。**既知の例外:** yt-dlp / ffmpeg は `releases/latest` 参照でバージョン固定がない。これは design.md「残存リスク（完全性 ≠ 真正性）」で明文化済みの受容済みトレードオフ |
| SECURITY-11 セキュアな設計 | **Compliant** | セキュリティに関わる判断を専用の純粋関数へ分離（`isValidURL` / `isM3U8URL` / `refererFor` / `isKillablePID` / `redactLine`）。多層防御（URL 検証 + `--` 終端 + 導出結果の再検証）。レート制限は公開エンドポイントがないため N/A。**誤用ケースの考慮**: プレイリストエントリの URL はリモート（動画サイト側）が制御できる値であることを design.md で明示し、同じ検証経路を通す |
| SECURITY-12 認証・資格情報管理 | N/A | ユーザー認証機構を持たない。ソース・設定にハードコードされた資格情報はない |
| SECURITY-13 ソフトウェア・データの完全性検証 | **Compliant** | yt-dlp / ffmpeg はダウンロード後に SHA256 照合し、一致した実体のみ `os.Rename` で原子的に配置。JSON は型付き構造体へデコード（任意型の復元をしない）。外部 CDN からのスクリプト読み込みがないため SRI は N/A |
| SECURITY-14 アラートと監視 | N/A | 監視基盤を持たないローカルアプリ。`moviedl.log` は起動時に切り詰める調査用ログで、監査ログではない |
| SECURITY-15 例外処理とフェイルセーフ | **Compliant** | 外部呼び出し（HTTP・ファイル I/O・プロセス操作）はすべて明示的にエラー処理。**フェイルクローズ**: `UpdateYtDlp` はプロセス排出を確認できなければ置き換えを**中止して**アイテムを待ちキューへ戻す。リソース後始末: `workDir` は `defer os.RemoveAll`、ジョブハンドルは `defer releaseProcessTree`、`uniqueDest` の予約は rename 失敗時に `os.Remove`。新規の純粋関数は不正入力に対し `""` またはエラーを返して先へ進まない |

**Blocking SECURITY findings: なし**

---

## PBT コンプライアンス（Property-Based Testing / Full 強制）

| ルール | 判定 | 根拠 |
|---|---|---|
| PBT-01 プロパティ特定 | **Compliant** | design.md「m3u8 / HLS」テスト可能プロパティ表（12 プロパティ）と「プロセス管理」の表に、カテゴリ付きで記載。`code-generation-plan-m3u8.md` から参照 |
| PBT-02 ラウンドトリップ | N/A | 今回追加した操作に逆関数を持つものがない。`redactLine` は**意図的に非可逆**（トークンを復元できてはならない） |
| PBT-03 不変条件 | **Compliant** | クエリ非依存（`isM3U8URL` / `m3u8FileName`）、`-` 始まりを返さない（`refererFor`）、オリジンのみ（`refererFor`）、安全なファイル名（`m3u8FileName`）、URL なし行の保存（`redactLine`）、範囲制約（`isKillablePID`）を PBT で検証 |
| PBT-04 冪等性 | **Compliant** | `TestPropRedactLineIdempotent`（`redactLine(redactLine(s)) == redactLine(s)`）。ログ経路が増えて二重適用されても壊れないことを保証 |
| PBT-05 Oracle | N/A（新規分） | 今回の新規関数に参照実装がない。既存の `TestPropSelectToStartCountMatchesOracle` は引き続き稼働 |
| PBT-06 ステートフル PBT | N/A | 新規コードは純粋関数と OS のプロセス操作。プロセスツリーの挙動はコマンド列のモデルで再現できず、**実プロセスを起こす統合テスト 7 件**（上記）で代替している |
| PBT-07 ジェネレータ品質 | **Compliant** | URL はスキーム・ホスト・ポート・パス要素・クエリから**構造的に組み立てる専用ジェネレータ**（`drawM3U8URL` / `genAuthority` / `genPathSegments`）。汎用語（`hls` など）と大小混在の拡張子を混ぜて境界を踏む。敵対的入力（`--exec=` / `-J` / スキーム欠落）も `genAdversarialURLish` で混入。生プリミティブのみのジェネレータは URL 型の引数に使っていない |
| PBT-08 shrink と再現性 | **Compliant** | rapid のデフォルト shrink を無効化していない。失敗時に `-rapid.seed=N` が出力される。CI は `RAPID_SEED: ${{ github.run_id }}` を記録（`.github/workflows/ci.yml`） |
| PBT-09 フレームワーク選定 | **Compliant** | `pgregory.net/rapid v1.3.0`（`go.mod`）。前回要件で確定済みで再選定なし |
| PBT-10 例示ベースとの併存 | **Compliant** | `pbt_test.go` を分離。新規の全関数（`isM3U8URL` / `refererFor` / `m3u8FileName` / `resolveTitle` / `redactLine` / `logLine` / `isKillablePID` / `isYtDlpErrorLine` / `explainDownloadError`）に例示ベーステストが存在し、期待値を具体的に固定している |

**Blocking PBT findings: なし**

---

## 既知の残存事項

1. **Windows 実機の動作未検証**（上記の警告を参照）。主目的の達成が未確認
2. **Job Object 割り当ての競合窓**: `cmd.Start()` から `AssignProcessToJobObject` までの間に
   yt-dlp が子を産むとジョブに入らない。実用上は問題にならない見込みだが、厳密には閉じていない
   （`os/exec` がスレッドハンドルを公開しないため `CREATE_SUSPENDED` 方式が取れない）
3. **ライブ配信は対象外**: 停止時に成果物が 0 バイトになる非対称性を design.md に記録済み。
   「停止して保存」を実装するなら孫の ffmpeg へ `SIGINT` / `SIGTERM` を届ける設計が別途必要
4. **視聴ページが別ドメインのサイト**では自動導出した Referer が効かない。
   「元ページ URL をユーザーが指定して再試行」は今回の対象外
5. **エラー文言の変更は m3u8 以外にも及ぶ**（意図した改善）。失敗理由が `"exit status 1"` から
   yt-dlp の実際のエラー行に変わる
