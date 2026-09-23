# AI-DLC Audit Log

## Initial User Request
**Timestamp**: 2026-08-06T00:00:00Z
**User Input**: "新しい要件を追加したい。同時ダウンロード数として0を"
**AI Response**: "Welcome message を表示し Workspace Detection を実行（Brownfield と判定）。入力が文中で途切れていたため、0 の意味を確認する質問を準備した。"
**Context**: INCEPTION - Workspace Detection

---

## Requirements Clarification (User)
**Timestamp**: 2026-08-06T00:00:00Z
**User Input**: "同時ダウンロード数として1以上が選べるようになっているのですが、そこにゼロも許容するようにして、ゼロの場合はURLの登録だけを許容しダウンロードは行わない状態にして欲しいです"
**AI Response**: "要件の主旨を確定（同時ダウンロード数の選択肢に 0 を追加、0 のときは URL 登録のみ可・ダウンロードは行わない）。残る曖昧点（0 切替時の実行中アイテムの扱い、手動開始の可否、既定値と永続化、Reverse Engineering の実施可否、拡張 opt-in）を requirement-verification-questions.md にまとめて回答待ちとした。"
**Context**: INCEPTION - Requirements Analysis Step 6

---

## Workspace Detection
**Timestamp**: 2026-08-06T00:00:00Z
**Findings**:
- 既存コード有り（Go + Wails v2、フロントエンドは素の HTML/JS）→ Brownfield
- `aidlc-docs/aidlc-state.md` は存在しなかったため新規作成
- `aidlc-docs/inception/reverse-engineering/` は無いが、`aidlc-docs/inception/requirements/requirements.md` と `aidlc-docs/inception/application-design/design.md` にアーキテクチャ・自動補充ルール・既知のピットフォールが文書化済み
- 該当実装箇所: `app.go` `SetMaxConcurrent`/`GetMaxConcurrent`/`selectToStart`/`scheduler`、`frontend/index.html` の同時ダウンロード数プルダウン
**Context**: INCEPTION - Workspace Detection 完了

---

## Requirements Analysis — 質問提示
**Timestamp**: 2026-08-06T00:00:00Z
**AI Response**: "`aidlc-docs/inception/requirements/requirement-verification-questions.md` を作成し、Q1 0 切替時の実行中アイテムの扱い、Q2 0 のときの手動開始の可否、Q3 既定値と永続化、Q4 Reverse Engineering の実施可否、Q5 Security 拡張 opt-in、Q6 Property-Based Testing 拡張 opt-in を質問。GATE により回答待ちで停止。"
**Context**: INCEPTION - Requirements Analysis Step 6 GATE

---

## Requirements Analysis — 質問への回答
**Timestamp**: 2026-08-06T00:00:00Z
**User Input**: "書いたよ"
**User Answers (requirement-verification-questions.md の生の記述)**:
- Q1 `[Answer]:当然Aです。もともとダウンロード中の数を減らしても実行中のダウンロードは停止しない実装になっているはずです。`
- Q2 `[Answer]:A 当然許可します。これも今と同じです。手動開始で今の最大同時ダウンロード数を超えても実行することになっています。`
- Q3 `[Answer]:C`
- Q4 `[Answer]:今回の要件を既存の要件に追加してください。ゼロが増えるだけで基本的な要件は変わっていないので影響は少ないはずです。`
- Q5 `[Answer]:今回はスキップ`
- Q6 `[Answer]:今回は実行`
**AI Response**: "回答を Q1=A / Q2=A / Q3=C / Q4=A（Reverse Engineering スキップ・既存 requirements.md へ追記）/ Q5=B（Security 拡張 opt-out）/ Q6=A（PBT 拡張 opt-in・Full 強制）と解釈。`property-based-testing.md` をロードし、`aidlc-state.md` の Extension Configuration を更新。`requirements.md` の「キュー」節と「手動開始」節を更新し、0〜10 の範囲・既定 0・登録のみモードの詳細を追記。"
**Context**: INCEPTION - Requirements Analysis Step 6 GATE 通過 → Step 7 要件文書更新

---

## Requirements Analysis — 承認プロンプト提示
**Timestamp**: 2026-08-06T00:00:00Z
**AI Response**: "requirements.md 更新完了。User Stories はスキップ推奨（既存 UI の選択肢に 0 を追加する単一コンポーネントの小規模拡張）とし、承認プロンプトを提示。永続化について Q3 の回答が明言していなかったため「永続化なし・起動時は常に 0」と解釈した旨を明記して確認を求めた。"
**Context**: INCEPTION - Requirements Analysis Step 9（承認待ち）

---

## Requirements Analysis — 承認
**Timestamp**: 2026-08-06T00:00:00Z
**User Input**: "実装して"
**AI Response**: "要件を承認（Approve & Continue）かつ実装まで進める指示と解釈。User Stories はスキップ（既存 UI の選択肢に 0 を追加する小規模変更・ユーザー影響は既存要件の範囲内）。永続化なし・起動時は常に 0 の解釈も承認されたものとして扱う。Workflow Planning → Application Design（design.md 更新・PBT-01 プロパティ特定）→ Code Generation（TDD）→ Build and Test を連続実行する。"
**Context**: INCEPTION - Requirements Analysis 承認 → Workflow Planning へ

---

## Workflow Planning
**Timestamp**: 2026-08-06T00:00:00Z
**AI Response**: "`aidlc-docs/inception/plans/workflow-plan.md` を作成。実行ステージ: Application Design（minimal・design.md 追記）/ Units Generation スキップ（単一ユニット）/ Functional Design は Application Design に統合（minimal）/ NFR Requirements は PBT-09 のフレームワーク選定のみ（`pgregory.net/rapid` 確定）/ NFR Design・Infrastructure Design はスキップ / Code Generation（TDD）/ Build and Test。"
**Context**: INCEPTION - Workflow Planning

---

## Application Design（minimal）
**Timestamp**: 2026-08-06T00:00:00Z
**AI Response**: "`design.md` を更新。(1)`maxActive` の契約を 0〜10・既定 0 に、(2)新節「maxActive == 0（登録のみモード）」で 0 専用分岐を追加しない理由・抑止対象は scheduler のみ・実行中を停止しない理由・既定を 0 にする理由を明文化、(3)新節「テスト可能プロパティ（PBT-01）」で 7 プロパティとフレームワーク `pgregory.net/rapid` を記録。Units Generation / Functional Design / NFR Design / Infrastructure Design はスキップ。"
**Context**: INCEPTION - Application Design + CONSTRUCTION - NFR Requirements（PBT-09）

---

## Code Generation（TDD）
**Timestamp**: 2026-08-06T00:00:00Z
**AI Response**: "TDD 5 サイクルを実行。サイクル 1: Red（`SetMaxConcurrent(0)` が 1 になる／`NewApp` が 1 を返す、4 件失敗）→ Green（クランプ下限 1→0、`maxActive: 0`）→ Refactor（doc コメント更新）。サイクル 2: `selectToStart` の maxActive=0 と `SetMaxConcurrent(0)` の非破壊性の回帰テストを追加、追加時点で緑（既存実装が一般ルールで要件を満たしていたため実装変更なし）。サイクル 3: `pgregory.net/rapid v1.3.0` を追加し `pbt_test.go` に 7 プロパティを実装、クランプ下限を一時的に 1 に戻すミューテーションで PBT が失敗しシードが出力されることを確認（実装は復元）。サイクル 4: `frontend/index.html` のプルダウンを 0 から生成し 0 のラベルを『0（登録のみ）』に。サイクル 5: CI に `RAPID_SEED` を追加、`.gitignore` に `testdata/rapid/` を追加。"
**Context**: CONSTRUCTION - Code Generation

---

## Build and Test
**Timestamp**: 2026-08-06T00:00:00Z
**AI Response**: "`gofmt -l .` 差分なし、`go vet ./...` 警告なし、`staticcheck ./...` 警告なし、`go test -race ./...` = ok moviedl 1.614s、`make check` 成功。PBT 7 件すべて 100 ケース通過。`build-and-test-summary.md` を作成（PBT のシード再現手順・CI 統合・フロントエンド手動確認チェックリストを含む）。PBT Compliance を aidlc-state.md に記録（PBT-02/PBT-06 は N/A、他は Compliant、blocking findings なし）。"
**Context**: CONSTRUCTION - Build and Test（完了・承認待ち）

---

## リリース（v0.2.1）
**Timestamp**: 2026-08-06T00:00:00Z
**User Input**: "大丈夫そうなんでコミットしてリリースして"
**AI Response**: "バージョン（v0.2.1）とコミット先（main 直接）を確認のうえ、35c8767 を main へ push し v0.2.1 タグを push。CI 成功、Release ワークフロー成功（moviedl-macos.zip / moviedl-windows.zip 公開）。build/windows/ は Wails 生成物かつ本変更と無関係のためコミットに含めなかった旨を報告。"
**Context**: 🟡 OPERATIONS - リリース完了

---

## 新規要件（yt-dlp の更新）— 提起と実現性の議論
**Timestamp**: 2026-08-06T00:00:00Z
**User Input**: "このソフトウェアはダウンロード機能を持った外部ライブラリに強く依存していると思います。実質はそのライブラリのラッパーです。ということは、このツール以上にそのライブラリを定期的にアップデートするのが大事だと思うんです。それが事実ならば、そのライブラリをアップデートする機能が必要だと思いませんか？またmac版はバイナリに内包していますが、これも内包しないほうがいいってことになりませんかね？"
**AI Response**: "前半に同意し、実装を確認して欠落を提示（InstallYtDlp は latest 取得 + SHA256 照合 + 原子的 rename を既に持つが、バナーが未インストール時のみ表示されるため更新の起点がない／バージョン表示も新版チェックもない）。後半の前提は誤りである旨を事実で訂正（go:embed はフロントエンド資産のみ。yt-dlp は全 OS で os.UserConfigDir()/moviedl へダウンロード、macOS の ffmpeg は Homebrew 参照。既に非内包であり、それが更新機能を実装できる理由でもある）。Windows で実行中 exe を上書きできない制約と、退避リネーム方式という代替案を提示。"
**Context**: INCEPTION - Requirements Analysis（新規要件の意図分析）

---

## 新規要件（yt-dlp の更新）— 方式の決定
**Timestamp**: 2026-08-06T00:00:00Z
**User Input**: "今回は手動でいいし、自動的にチェックする必要もありません。アップデートを指示されたらそのプログラムを停止すればいいと思うんですけど、それで実現可能ですか？"
**AI Response**: "実現可能と回答。ただし (1) 一時停止はサスペンドでプロセスが生きており .exe を掴むため停止対象に含める必要がある、(2) FetchPlaylist / STEP2a のタイトル取得も yt-dlp を起こすが Go 側で追跡されていない、(3) workDir が毎回破棄されるため停止したダウンロードは 0% からやり直しになる、の 3 点を事実として提示。選択肢（退避リネーム方式 / 停止してから更新）を提示。"
**User Answers (AskUserQuestion)**:
- 更新方式 = `停止してから更新`
- 確認ダイアログ = `必要（影響がある場合のみ）`
**AI Response**: "回答に基づき requirements.md「yt-dlp の更新」節と design.md「yt-dlp の更新（UpdateYtDlp）」節を作成。手動のみ・自動チェックなし・影響ありのときのみ確認・停止は登録簿ベースで全経路・ダウンロードと検証の成功後に停止（ネットワーク失敗で進捗を失わせない）・更新完了後に queued へ戻す、を明文化。取得中プロセスが Go 側で追跡されていない実装ギャップに対し yt-dlp プロセス登録簿の導入を設計に追加。"
**Context**: INCEPTION - Requirements Analysis Step 7（要件・設計文書作成）→ 承認待ち

---

## 新規要件（yt-dlp の更新）— 承認と実装
**Timestamp**: 2026-08-22T00:00:00Z
**User Input**: "それで良いです"
**AI Response**: "要件・設計を承認と解釈し Code Generation（TDD 6 サイクル）を実施。実装中に 2 つの非自明な事象を発見し design.md へ追記: (1) runDownload 末尾の『error 以外は削除』判定により、更新のため再キューしたアイテムが消えるバグを予防（shouldRemoveWhenDone に切り出し queued も残す）、(2) Kill された yt-dlp の Wait エラーが error 扱いになるため DownloadItem.stopFlag を追加。requeueStopped から resetForRequeue を切り出し、emit（Wails ctx 必須）に触れず単体テストできるようにした。PBT 9 件を追加し合計 16 件。make check 成功。"
**Context**: CONSTRUCTION - Code Generation（完了）→ 手動確認待ち

---
## 新規要件（m3u8 対応）— 初期リクエスト
**Timestamp**: 2026-09-21T00:00:00Z
**User Input**: "m3u8という拡張子のファイルで示される動画をダウンロードできるようにできないか？"
**AI Response**: "Workspace Detection を実行（既存 aidlc-state.md を検出し Brownfield としてレジューム）。既存要件・設計・コードをロードし Requirements Analysis に進む。"
**Context**: INCEPTION - Workspace Detection（新規要件 3 の起点）

---

## 新規要件（m3u8 対応）— 意図分析と実測
**Timestamp**: 2026-09-21T00:00:00Z
**User Input**: "m3u8という拡張子のファイルで示される動画をダウンロードできるようにできないか？"
**AI Response**: "要求が曖昧（URL 形式かローカルファイルか不明）と判定し、推測で進める前に現状の挙動を実測。ローカルに HLS（VOD・320x240・3 断片、ffmpeg で生成した合成テスト映像）を立て python3 -m http.server で配信し、現行コードと同一引数の yt-dlp を実行した。結果: (1) FetchPlaylist 相当は成功し 1 エントリを返す（webpage_url = m3u8 URL、id/title = m3u8 のファイル名 'master'）、(2) buildYtDlpArgs と同一引数の本ダウンロードも成功し [hlsnative] Total fragments: 3 → [FixupM3u8] を経て mp4 が生成された。すなわち https 形式の m3u8 URL はアプリ無変更で既にダウンロード可能。加えて #EXT-X-ENDLIST のない m3u8 は is_live: true / duration: null と判定されることも実測。コード読解で isValidURL が http/https+ホストを要求するためローカル .m3u8（file:// および素のパス）は入口で拒否されることを確認。以上から埋めるべき穴を 4 点（保存ファイル名が master.mp4 に集中／ローカルファイル拒否／ライブ停止時に録れた分が残らない／Referer・Cookie を渡す手段なし。4 点目のみ未実測の推定と明記）として整理し、requirement-verification-questions.md に Q1〜Q6（Q5/Q6 は拡張オプトイン）を作成。既存の同ファイル（要件 1 の Q&A）は requirement-verification-questions-archive.md へ git mv して保全。"
**Context**: INCEPTION - Requirements Analysis Step 2〜6（質問提示・GATE で停止）

---

## 新規要件（m3u8 対応）— スコープ確定と実現可能性評価
**Timestamp**: 2026-09-21T00:00:00Z
**User Input**: "インターネットに置いてあるファイルです。これをダウンロードしたいんです。現実的に実行可能なのかを先に考えて欲しいです"
**AI Response**: "Q1 を『インターネット上の m3u8 URL』と確定（ローカル .m3u8 対応は対象外）。要件確定より先に実現可能性を判定するため、python3 でテストサーバを立て実サイトの配信条件を再現し、buildYtDlpArgs と同一引数で 9 シナリオを実測した（素材は ffmpeg 生成の合成映像 testsrc+sine、実在コンテンツ不使用）。成功: 素の VOD（master/media 両方）、AES-128 暗号化 HLS（鍵 URI から自動復号）、映像音声別レンディション（Merger で結合）。失敗: Referer 必須サーバ（403）→ --referer 付与で解消、期限切れ URL（403・対策不能）、断片 1 つ 403（--abort-on-unavailable-fragment が設計どおり中断）。ライブは自然終了しない。

★重大な発見: ライブ HLS は yt-dlp が ffmpeg を子プロセスとして起動し委譲する（実測 PID 57658 の子 57660）。一方アプリの停止系はすべて item.cmd = yt-dlp のみを対象とし、applyOSProcAttr は非 Windows で no-op でプロセスグループを作らない。決定的実測: yt-dlp だけに SIGKILL → ffmpeg は PPID 1 の孤児として生存し取得を継続。よってキャンセルは帯域を食い続け、一時停止は実際には止まらず、waitProcsDrained は ffmpeg が掴んだままでも排出完了と誤判定する。これは m3u8 固有ではなく既存バグ（VOD でも Merger/FixupM3u8 中のキャンセルで同経路）で、design.md『停止対象は実行中だけでは足りない』の 3 経路に孫プロセスとして漏れていた。検証中に実際に孤児 ffmpeg が 2 プロセス残留したため手動で後片付けした。

さらに中断時の残骸を実測: VOD（hlsnative）の .part は 59,220 バイトの valid な MPEG-TS で ffprobe が読め ffmpeg -c copy で救出可能。ライブ（ffmpeg）の .part は SIGKILL では 0 バイトかつ Invalid data で救出不能。よって『停止して保存』は VOD なら現実的、ライブには graceful stop が必須。

以上を m3u8-feasibility.md に文書化し、スコープを 3 段（第1段: Referer/ファイル名/期限切れ案内、第2段: 孤児化修正=独立要件、第3段: ライブ録画）に分割して第1段のみを推奨。requirement-verification-questions.md を実測結果に合わせて Q1〜Q6 に作り直した（Q1 をスコープ選択に差し替え）。"
**Context**: INCEPTION - Requirements Analysis（実現可能性評価完了・Step 6 GATE で回答待ち）

---

## 新規要件（m3u8 対応）— Q1 の言い直しとスコープ確定
**Timestamp**: 2026-09-21T00:00:00Z
**User Input**: "Q1がどれなのかよくわかりません"
**AI Response**: "Q1 の設問が不良だったと認め、AI が勝手に作った『第1段／第2段』という語を捨てて平易に言い直した。あわせて『まず手元の m3u8 URL を今のアプリで試せば Q1 は自動的に決まる』という判定表（成功だがファイル名不明→A / 403→A / 失効→A / 終わらない→ライブなので D）を提示。推奨は A（既存の停止処理に触らず回帰リスクが低い）とした。"
**Context**: INCEPTION - Requirements Analysis（Q1 の再提示）

---

## 新規要件（m3u8 対応）— Q1 回答
**Timestamp**: 2026-09-21T00:00:00Z
**User Input**: "Bをやってほしい"
**AI Response**: "Q1 = B（AI 推奨の A ではなくユーザー判断の B）を確定として記録。スコープは (1) 保存ファイル名の改善 (2) Referer の付与 (3) 期限切れ URL の案内 (4) ffmpeg 孫プロセスが停止されない既存バグの修正（CancelDownload / PauseDownload / UpdateYtDlp の waitProcsDrained の 3 経路）。対象外はライブ配信の録画とローカル .m3u8 の読み込み。残る Q2〜Q6 に実測と既存設計に基づく推奨を記入した: Q2=B（URL だけから決まる純粋関数で命名。C/D の入力欄はドロップ経路で入力機会がないため不採用）、Q3=B ただし適用をパスが .m3u8 で終わる URL に限定（既存 1000 サイトの経路に触らないデグレ防止）、Q4=D（A+C。B は design.md が禁じる maxActive==0 の特別分岐になるため不採用）、Q5=A（前回はスキップだったが今回は --referer の値が新たな引数インジェクション面になり、かつ停止方法自体を変更するため Yes を推奨）、Q6=A（純粋関数が 3 つ増えるため）。"
**Context**: INCEPTION - Requirements Analysis（Q1 確定・Q2〜Q6 の推奨提示）

---

## 新規要件（m3u8 対応）— Q2〜Q6 承認と要件文書の作成
**Timestamp**: 2026-09-22T00:00:00Z
**User Input**: "進めて"
**AI Response**: "Q2=B / Q3=B / Q4=D / Q5=A / Q6=A を推奨どおり確定として記録。Q5=A・Q6=A により opt-in となったため security-baseline.md と property-based-testing.md をロード（Deferred Rule Loading）。Security Baseline は要件 1・2 では No だったが要件 3 で Yes に変更（理由: Referer という新しい文字列を yt-dlp のコマンドラインに渡すため引数インジェクション面が拡大する／プロセスの停止方法そのものを変更する）。

requirements.md に「m3u8（HLS）対応」節を新設（既存要件への追記方針を継続）。内容: 意図分析（Enhancement + Bug Fix / Multiple Components / Moderate）、受け入れ方法（m3u8 専用経路を作らない・0 の例外を作らない）、保存ファイル名（URL だけから決まる純粋関数で自動生成）、Referer（.m3u8 で終わる URL 限定でオリジンを送る。限定自体を要件として明文化＝デグレ防止）、期限切れ URL（登録時警告 + 失敗時説明。0 の特別分岐は禁止）、停止操作が子プロセスに及ぶこと（既存不具合の修正・3 経路）、セキュリティ要件、対象外の表。

セキュリティ拡張の有効化に伴い app.go のログ実装を確認し、新たな該当事象を 1 件発見して要件に反映: moviedl.log（ユーザー設定フォルダ内・0644・平文）に実行コマンド全体と yt-dlp 出力行が追記されており、有効期限トークン付き URL がクエリ文字列ごと記録される。m3u8 の URL はトークンが実質的な認可情報であるため SECURITY-03（ログに機微情報を出力しない）に該当すると判断し、『ログに書く URL はクエリ部分をマスクする』を要件に追加した。

aidlc-state.md を更新: Extension Configuration を要件 3 の設定に更新し過去 2 要件の設定を記録として保持、Stage Progress を要件 3 用に作り直し（要件 1・2 の進捗は別節へ移して保全）、Current Stage を Requirements Analysis 完了・承認待ちに更新。"
**Context**: INCEPTION - Requirements Analysis Step 7〜9（要件文書作成・承認待ち）

---

## 新規要件（m3u8 対応）— 要件承認と Workflow Planning
**Timestamp**: 2026-09-22T00:00:00Z
**User Input**: "作業を続けて"
**AI Response**: "要件承認と解釈し Requirements Analysis を APPROVED として記録。User Stories は SKIP と判定（新しいペルソナもユーザーワークフローも生じず、可視の変更は保存名の規則・警告文・エラー文言の 3 点で既存フロー内の表示変更のみ。バグ修正部分は再現手順が実測で確定済み）。Workflow Planning を実行し workflow-plan-m3u8.md を作成（既存の workflow-plan.md は要件 1 用のため上書きせず要件別に分離）。

リスクを Medium〜High と評価し、その所在をプロセス停止方法の変更 1 点に特定した: (1) 過去に罠を踏んだ CancelDownload / PauseDownload / UpdateYtDlp に触る（design.md に明文化済みの領域で CLAUDE.md デグレ防止の対象）、(2) 非 Windows のプロセスグループと Windows の Job Object で実装が完全に分かれ Windows 側を開発機 macOS で検証できない、(3) 停止範囲を広げるため無関係プロセスへの波及の危険。低減策として m3u8 の機能追加（純粋関数中心・低リスク）と停止の修正（高リスク）を別コミットに分離し停止の修正を最後に単独で行う方針を決定。

ステージ判定: Application Design を EXECUTE / standard（要件 1・2 の minimal から格上げ。理由は (a) プロセスグループ登録が applyOSProcAttr や -- 終端と同じ『1 箇所でも漏れたら穴が空く』新ルールになる、(b) design.md の『停止対象は実行中だけでは足りない』節が孫プロセスを扱っておらず誤りの訂正が必要、(c) PBT-01、(d) CLAUDE.md が義務化するピットフォール記録）。NFR Requirements は部分実行 / minimal（PBT-09 は rapid で既定済み）。NFR Design は SKIP（セキュリティ要件 5 件は論理コンポーネント設計ではなく具体的な実装規則のため design.md に直接記載する判断）。Infrastructure Design・Units Generation・Functional Design は SKIP。Build and Test は standard（Windows 実機の手動確認が必要なため minimal にしない）。

規模は約 12 TDD サイクル（コミット A 7 + コミット B 5）と見積もり。触ってはいけないもの（uniqueDest / -o ランダム方式 / -- 終端 / --abort-on-unavailable-fragment / shouldRemoveWhenDone / maxActive==0 の一般ルール）を計画に明示。content-validation.md に従い Mermaid をスクリプトで構文検証（16 ノード・未定義参照なし）し、テキスト代替も併記した。"
**Context**: INCEPTION - Workflow Planning（完了・承認待ち）

---

## 新規要件（m3u8 対応）— Application Design 〜 Build and Test
**Timestamp**: 2026-09-23T00:00:00Z
**User Input**: "どんどん進めて"
**AI Response**: "Workflow Plan 承認と解釈し Application Design → NFR Requirements → Code Generation → Build and Test を連続実行した。

Application Design（standard）: design.md に (1)「m3u8 / HLS」節（判定を isM3U8URL 1 つに集約／Referer は m3u8 限定＝デグレ防止／保存名は URL だけから組み立てる／ログのトークンマスク／期限切れ URL／PBT-01 プロパティ 12 件）と (2)「プロセス管理（停止は孫プロセスまで及ばせる）」節（ライブ HLS が ffmpeg に委譲される実測／OS ごとの 6 関数抽象／kill(-pid) の符号が致命的に危険／Windows の Job Object と割り当て競合窓／ライブ停止時に .part が 0 バイトになる非対称性）を追加。既存「停止対象は『実行中』だけでは足りない」節の取りこぼし経路を 3 → 4（孫プロセス）に訂正した。

Code Generation: TDD 13 サイクル（Red の失敗を毎回 go test で確認 → Green → Refactor）。追加した純粋関数は isM3U8URL / refererFor / m3u8FileName / meaningfulPathSegment / resolveTitle / redactLine / logLine / isKillablePID / isYtDlpErrorLine / explainDownloadError。OS 抽象は applyOSProcAttr(Setpgid) / trackProcessTree / killProcessTree / suspendProcessTree / resumeProcessTree / releaseProcessTree / processTreeGone を両 OS に実装。停止経路（stopTargets / killRegisteredProcs / PauseDownload / ResumeDownload / CancelDownload）をツリー版に切り替え、UpdateYtDlp STEP4 で登録簿の排出とツリー消滅の両方を待つようにした。フロントエンドは m3u8 の期限切れ注意バナーと IsM3U8URL 経由の判定一本化。

計画からの差異 7 件を code-generation-plan-m3u8.md に記録。主なもの: (a) A7 を各呼び出し側の redactLine から logLine 内での一括マスクに変更（『1 箇所でも漏れたら穴が空く』ルールを増やさないため）、(b) 計画外サイクル A8 を追加 — 要件『403 で URL 失効を案内』を実装しようとして item.Error に cmd.Wait の "exit status 1" しか入っておらず 403 を判別する材料が存在しないことが判明したため、ERROR 行の捕捉と explainDownloadError を追加（副作用として m3u8 以外でも失敗理由が実際のエラー行に変わる。意図した改善）、(c) processTreeGone / waitTreesGone の追加、(d) isKillablePGID → isKillablePID に改名し app.go へ移動（Windows でも使うため）、(e) IsM3U8URL の Wails バインディング追加、(f) Windows はジョブ作成失敗時に従来の単一プロセス停止へフォールバックする設計（macOS で検証不可能なため悪化させない）、(g) コミットは未実施。

Build and Test: make check 成功（gofmt 差分なし / go vet 警告なし / staticcheck 警告なし / go test -race ok）。テスト 67 件通過（例示ベース 41 + PBT 26。PBT は 16 → 26 に増加）。GOOS=windows の build と vet も通過。非 Windows は実プロセスを起こす統合テスト 7 件を追加し、孤児化の回帰（子まで死ぬこと）と対照実験（リーダーだけ Kill すると子が生き残ること）を固定した。SECURITY コンプライアンス 15 ルール・PBT コンプライアンス 10 ルールを評価し blocking findings なし（SECURITY-09 はエラー文言に作業ディレクトリのパスが含まれうる点を注記付き Compliant、SECURITY-10 は releases/latest のバージョン非固定を既知の受容済み例外として記録）。

未完了として明示: macOS / Windows 実機の手動確認。とくに Windows の Job Object は開発機で動作検証できず、『孫プロセスまで停止が及ぶ』という主目的が Windows で達成されているかは未検証である。"
**Context**: INCEPTION Application Design → CONSTRUCTION Code Generation → Build and Test（自動検証完了・実機手動確認待ち）

---

## 不具合報告と切り分け（v0.2.3 後）
**Timestamp**: 2026-09-23T00:00:00Z
**User Input**: "期待する動作にならなかったんで、あなたにログを渡して解析してもらいたいんですけど、どうしたらいい？" / "期待した動作は当然ダウンロードできること。実際におきたのは、URLを渡したがダウンロードがはじまりもせずにダウンロードリストから消えたこと。" / "https://www.javxxx.me/ja/v2/video/mmus-101/" / "検索して偶然出てきたm3u8ファイルがあるサイトのURLを渡しました。このサイトはm3u8そのものではなく、そこにリンクされているのだと思います。m3u8ではない場合は動画ファイルを探してダウンロードしてくれるのになぜこのパターンだと追いかけてくれないのでしょうか"
**AI Response**: "ログの実態を確認したところ 0 バイトで、truncateLog が startup で毎回消していることと、登録経路（FetchPlaylist / AddToQueue）が一切ログを書いていないことを特定。コード読解により、フロントエンドの削除判定は finished / cancelled のみで、登録成功なら emit で必ずリストに残るため、『開始もせず消えた』のは AddToQueue が拒否して空文字を返した（不正 URL または重複）ケースだと絞り込んだ。

渡された URL のパスは /ja/v2/video/mmus-101/ で .m3u8 で終わらないため isM3U8URL が偽になり、v0.2.3 の m3u8 処理が一切発動していなかったことを指摘。要件確定時（Q1）に『m3u8 の URL』と『HLS 配信ページの URL』の区別を掘らなかった点は AI 側の詰めの甘さとして明示した。当該ドメインは商業作品の無許諾配信サイトと思われるため、そこからのダウンロードを成立させるための調査（URL 探索・yt-dlp 実行）は行わないと伝えた。

『なぜ m3u8 以外なら追いかけるのか』という質問に対し、ローカルに 2 種類のページ（m3u8 の URL が HTML に文字列として存在するもの / JS が実行時に組み立てるもの）を作って実測。前者は extractor=html5 で成功、後者は ERROR: Unsupported URL。動画も配信方法も同一で違いは『URL が JS 実行前の HTML に存在するか』だけであることを実証した。yt-dlp は JavaScript を実行しないため、これは m3u8 か mp4 かの違いではないと説明。選択肢 A（Referer のユーザー指定）/ B（登録の診断性）/ C（ヘッドレスブラウザ。design.md の WebView 規約に反するため高コスト）を提示し B → A を推奨した。"
**Context**: 不具合の切り分け（v0.2.3 後）→ 要件 4・5 の起点

---

## 要件 4・5 — 実装（B: 登録の診断性 → A: Referer のユーザー指定）
**Timestamp**: 2026-09-23T00:00:00Z
**User Input**: "じゃぁあなたの提案通りBをやってからAもやってください。"
**AI Response**: "requirements.md に要件 4『登録が拒否された理由をユーザーと調査者に伝える』と要件 5『元ページ URL を指定して再試行』を追記し、対象外に『ページ URL から JavaScript 実行後の m3u8 を発見する』を実測根拠付きで明記。design.md に『拒否した理由を返す（沈黙させない）』『ログのセッション管理』『Referer のユーザー指定（元ページ URL）』の 3 節と PBT-01 プロパティ表を追加。

TDD 6 サイクル（各サイクルで go test の赤を確認）。B: AddResult 型 / addRejection / addRejectionMessage を追加し AddToQueue の戻り値を string から AddResult へ変更、maxLogBytes / shouldRotateLog / startLogSession / appendLog を追加して truncateLog を廃止（起動でログを消さず上限 5 MiB 超過時だけ 1 世代退避）、FetchPlaylist に開始・失敗（ExitError.Stderr 含む）・解析失敗・成功件数・各エントリのログを追加。A: effectiveReferer を追加、buildYtDlpArgs に refererOverride 引数を追加（既存 8 箇所の呼び出しを更新）、DownloadItem.Referer を追加、RetryDownload の初期化を resetForRetry へ切り出して RetryWithReferer と共有。

フロントエンド: AddToQueue の戻り値を無視できない薄いラッパ addToQueue と非ブロッキング通知バナー showNotice（textContent で挿入し innerHTML を使わない）、エラーアイテムの ⤴ ボタンと元ページ URL 入力モーダル。

実装中の判断 3 件をプランに記録: (1) emit に nil ctx ガードを追加（startup 前は通知先が存在せず、これがないと App のメソッドを単体テストできない）、(2) logDirOverride というテスト用の差し替え口を導入（テストが実ログを汚すと、まさに信頼性を上げようとしているログをノイズで壊す）、(3) ログを『消さない』ではなく『上限超過で 1 世代退避』にした（際限ない膨張を防ぎ、消す経路を 1 箇所に限る）。

PBT を 6 件追加（addRejection の Oracle、addRejectionMessage の沈黙禁止、shouldRotateLog の単調性、effectiveReferer の '-' 始まり禁止・指定の優先・フォールバック）。make check 成功、81 テスト通過（PBT 32 + 例示ベース 49。前回 67 件から +14）。GOOS=windows の build と vet も通過。実機の手動確認とコミットは未実施。"
**Context**: CONSTRUCTION - Code Generation / Build and Test（要件 4・5 完了・手動確認待ち）

---

## 作業 6: リポジトリ構成の整理
**Timestamp**: 2026-09-23T00:00:00Z
**User Input**: "ディレクトリ構成がきたなくないですか？これが普通なの？" / "全部やって"
**AI Response**: "構成を調べ、普通なもの（Wails 標準の Go ルート直置き、ツール用フォルダ、gitignore 済みの実行ファイル）と本物の問題（app.go が 2065 行で全機能を抱えている／aidlc-docs の成果物名が要件ごとにバラバラ）を切り分けて報告し、A（app.go の役割別分割）→ B（成果物名の統一）を提案。承認を受けて両方を実施した。

A: go/ast で宣言ごとの行範囲（doc コメント込み）を取る解析ツールを作業用に作り、app.go の 114 宣言を 12 ファイル（app / queue / download / progress / urls / filename / workdir / logging / procs / ytdlp / ffmpeg / checksum）へスクリプトで振り分けた。手作業の切り貼りを避けたのは取りこぼしを防ぐため。別名 import（wailsruntime / goruntime / neturl）は goimports が推測できないため補った。分割前（git HEAD の app.go）と分割後で宣言本文を照合し 114 件が 1 文字も変わっていないことを確認。テストも同じ方法で app_test.go / helpers_test.go の 44 宣言を 12 個の *_test.go へ分け、本文の同一性とテスト一覧 81 件の完全一致を確認した。pbt_test.go は PBT-10（例示ベースと分離）に従い分割しなかった。make check と GOOS=windows の build / vet は通過。app.go は 222 行になった。

B: aidlc-docs の 6 ファイルを要件の略称を末尾に付ける規則に git mv で改名（要件 1 = concurrency-zero、要件 3 の質問 = m3u8、実現可能性 = feasibility-m3u8）。AI-DLC のフォルダ骨組み（inception / construction）は CLAUDE.md で最優先とされる構成規則なので変えていない。requirement-verification-questions.md は書かれた時期で指す中身が違うため一括置換せず、m3u8-feasibility.md への参照と、移動した関数を指す app.go へのリンク 3 件だけを張り替えた。作業記録（実装計画・テスト結果・audit.md）の本文中の旧ファイル名は、当時の記録なので書き換えていない。aidlc-docs/README.md（要件と成果物の対応表）を追加。aidlc-state.md は要件を足すたびに継ぎ足した結果、古い『承認待ち』『コミット未実施』などが残っていたため要件一覧を中心に書き直し、そこにしかなかった要件 1 の PBT 適合表は要件 1 のテスト結果ファイルへ内容を変えずに移した。Markdown 19 ファイルの相対リンクを機械的に検査し、実在のリンク切れがないことを確認した。

付随して見つけた既存の誤り: CONTRIBUTING.md が存在しない embed.go / embedded/ を載せ、『リリースビルドで ffmpeg を同梱』『macOS 版は universal』と書いていたが、release.yml を確認するとどちらも事実と異なった（ffmpeg は同梱せず、macOS は darwin/arm64 のみ）。実態に合わせて直した。また frontend/wailsjs/go/models.ts は前回手で追記した際に空行のタブを落としていたため、生成器の出力（タブ入り）が正として取り込んだ。"
**Context**: CONSTRUCTION - Code Generation（作業 6・動作変更なし）

---

