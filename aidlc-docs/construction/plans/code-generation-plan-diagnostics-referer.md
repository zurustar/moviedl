# Code Generation Plan — 登録の診断性（B）と Referer のユーザー指定（A）

- 要件: [requirements.md](../../inception/requirements/requirements.md)
  「登録が拒否された理由をユーザーと調査者に伝える」「元ページ URL を指定して再試行」
- 設計: [design.md](../../inception/application-design/design.md)
  「拒否した理由を返す（沈黙させない）」「ログのセッション管理」「Referer のユーザー指定（元ページ URL）」
- ユニット: moviedl-app（単一）

## 経緯

v0.2.3 のリリース後、ユーザーが URL を登録したところ**ダウンロードが開始されないまま
リストから消えた**。原因を調べようとしたが、画面にもログにも情報が残っておらず、
ユーザーも開発者も理由を特定できなかった。

切り分けの結果、渡された URL は動画ページの URL で**パスが `.m3u8` で終わっていなかった**ため、
v0.2.3 で追加した m3u8 の処理が一切発動していなかったことが判明した。さらに実測により
**yt-dlp は JavaScript を実行しない**ため、m3u8 の URL が HTML に文字列として存在しない
（JS が実行時に組み立てる）ページでは `Unsupported URL` になることを確認した。

この一件で露呈した本質的な問題は「**失敗の理由が誰にも分からない**」ことだった。
そこで B（診断性）→ A（Referer 指定）の順で対応する。

---

## B: 登録の診断性

### サイクル B-1: 拒否理由の構造化
- [x] Red: `TestAddRejection` / `TestAddRejectionMessage` / `TestAddToQueueReportsRejection` を書く。不正 URL と重複で理由と文言が返ること、不正判定が重複判定より先であること、未知の理由でも文言が空にならないことを表明 → 赤を確認
- [x] Green: `AddResult` 型、`addRejection`、`addRejectionMessage` を実装。`AddToQueue` の戻り値を `string` → `AddResult` に変更
- [x] Refactor

### サイクル B-2: ログのセッション管理
- [x] Red: `TestShouldRotateLog` / `TestStartLogSessionKeepsPreviousRun` / `TestAppendLog` を書く。**起動で前回のログが消えないこと**、上限超過時だけ 1 世代退避すること、`appendLog` もトークンをマスクすることを表明 → 赤を確認
- [x] Green: `maxLogBytes` / `shouldRotateLog` / `startLogSession` / `appendLog` を実装。`truncateLog` を廃止し `startup` の呼び出しを差し替え
- [x] Refactor: テストが実ログを汚さないよう `logDirOverride` を導入

### サイクル B-3: 登録経路のログ
- [x] Green: `FetchPlaylist` に開始・失敗（`ExitError.Stderr` を含む）・解析失敗・成功件数・各エントリのログを追加
- [x] Green: `AddToQueue` に受理・拒否のログを追加

### B-フロントエンド
- [x] `AddToQueue` の戻り値を確認する薄いラッパ `addToQueue` を追加（戻り値を無視できない形にする）
- [x] 拒否理由を非ブロッキングの通知バナーで表示（`showNotice`）。ドロップ経路で複数件が拒否されても操作を塞がない
- [x] プレイリスト選択からの一括登録でも理由を集めて一度に表示
- [x] 通知は `textContent` で入れる（`innerHTML` を使わない。XSS 対策）

---

## A: Referer のユーザー指定

### サイクル A-1: `effectiveReferer`
- [x] Red: `TestEffectiveReferer` を書く。指定が自動導出に勝つこと、m3u8 でない URL でも指定は使うこと、不正な指定は無視して自動導出に落ちること、**任意の入力で `-` 始まりを返さないこと**を表明 → 赤を確認
- [x] Green: 実装
- [x] Refactor

### サイクル A-2: `buildYtDlpArgs` の引数追加
- [x] Red: 既存 8 箇所の呼び出しを 5 引数に揃え、指定が `--referer` に渡ること・不正な指定は渡らないことを表明 → 赤（シグネチャ不一致）を確認
- [x] Green: `refererOverride` 引数を追加し `effectiveReferer` 経由にする
- [x] Refactor

### サイクル A-3: `RetryWithReferer`
- [x] Red: `TestRetryWithReferer` を書く。Referer を設定して待ちキューへ戻すこと、不正な指定を受け付けずアイテムを変えないこと、**Referer がリトライ・再キューで保持されること**を表明 → 赤を確認
- [x] Green: `DownloadItem.Referer` を追加、`RetryDownload` の初期化を `resetForRetry` へ切り出して共有、`RetryWithReferer` を実装
- [x] Refactor

### A-フロントエンド
- [x] エラーアイテムに「元ページ URL を指定して再試行」ボタン（⤴）を追加
- [x] 元ページ URL 入力モーダル（`#referer-modal`）を追加。既存の `.modal-overlay` を再利用
- [x] Go 側が返す検証エラーをモーダル内に表示する
- [x] 既に指定済みなら初期値として出す

---

## PBT（Property-Based Testing / Full 強制）
- [x] `addRejection`: Oracle（判定の一致）
- [x] `addRejectionMessage`: Invariant（**沈黙の禁止**）
- [x] `shouldRotateLog`: Invariant（範囲制約・単調）
- [x] `effectiveReferer`: Invariant（`-` 始まりを返さない / 指定の優先 / フォールバック）
- [x] design.md の PBT-01 表に追記

## 検証
- [x] `make check` 緑（`gofmt` / `go vet` / `staticcheck` / `go test -race ./...`）
- [x] **81 件通過**（PBT 32 + 例示ベース 49）。前回 67 件から +14
- [x] `GOOS=windows go build ./...` / `GOOS=windows go vet ./...` 通過
- [ ] 実機の手動確認（下記）
- [ ] コミット（ユーザーの指示があるまで行わない）

## 手動確認チェックリスト

- [ ] 不正な URL を登録 → **理由がバナーに出る**（黙って消えない）
- [ ] 同じ URL を 2 回登録 → 重複の理由がバナーに出る
- [ ] アプリを再起動 → `moviedl.log` の**前回分が残っている**（`session start` 行で区切られる）
- [ ] 対応していないページの URL を登録 → ログに `[FETCH] 失敗` と yt-dlp の stderr が残る
- [ ] エラーアイテムの ⤴ を押す → モーダルが開く。不正な URL を入れるとモーダル内にエラーが出る
- [ ] 妥当な元ページ URL を入れて再試行 → 待ちキューへ戻り、ログに `[RETRY]` が残る
- [ ] ログにトークン付きクエリが残っていない（`?<redacted>` になっている）

## 実装中の判断

### `emit` に nil ctx のガードを追加した

`AddToQueue` を単体テストしようとしたところ、`emit` が Wails の `EventsEmit` を呼び、
`startup` 前は ctx が nil のため失敗した。ctx が未設定なのは `startup` 前（= テスト）だけで、
その状態では通知先のフロントエンドが存在しない。`if a.ctx == nil { return }` を入れて
App のメソッドを単体テストできるようにした。

### `logDirOverride` というテスト用の差し替え口を入れた

ログのテストが実ログ（`~/Library/Application Support/moviedl/moviedl.log`）へ書くと、
**調査したい本物の記録をテストのノイズで汚す**。今回まさにログの信頼性を上げる作業なので、
それを損なうわけにはいかない。通常は空で、テストだけが設定する。

### ログを「消さない」ではなく「上限超過で 1 世代退避」にした

完全に消さない設計だと際限なく膨らむ。上限（5 MiB）を超えたときだけ `moviedl.log.1` へ
退避する。**ログを消す経路をここだけに限る**のが要点。
