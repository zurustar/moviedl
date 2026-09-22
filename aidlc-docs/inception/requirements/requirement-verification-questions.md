# 要件確認質問 — m3u8（HLS）対応

**対象要件（✅ 確定済み — Q1〜Q6 すべて回答済み）**: インターネット上に置かれた m3u8 が指す動画をダウンロードできるようにする

確定した要件は [requirements.md](requirements.md) の「m3u8（HLS）対応」節に転記済み。

**前提**: 実現可能性の評価は完了しています。判定と実測結果は
[m3u8-feasibility.md](m3u8-feasibility.md) を参照してください。結論は
**「実行可能。ただし第 1 層（素の VOD m3u8 URL）はすでに動いており、
第 3 層（ライブ）は既存の停止機構の欠陥を露出させる」** です。

**確定済み**:

- ~~Q: 「m3u8 という拡張子のファイル」はローカルファイルか URL か~~
  → **インターネット上の m3u8 URL**（2026-09-21 ユーザー回答）。ローカル .m3u8 ファイル対応は対象外とする

**回答方法**: 各質問の `[Answer]:` の後に記号（A/B/C/D/X）を書いてください。チャットで「Q1: A、Q2: B ...」と返答いただく形でも構いません。

---

## Q1: 今回、何を作りますか？（最重要）— ✅ 回答済み

A) m3u8 を普通に使えるようにするだけ（ファイル名・Referer・期限切れ案内）
B) **A + 見つけた既存バグも直す** — キャンセル・一時停止・yt-dlp 更新が **ffmpeg 孫プロセスに効いていない**問題も修正する（[m3u8-feasibility.md](m3u8-feasibility.md) 注目点 3）
C) 既存バグの修正だけ先にやる
D) 全部やる（A + B + ライブ配信の録画）
X) Other (please describe after [Answer]: tag below)

[Answer]: B（2026-09-21 ユーザー回答「Bをやってほしい」）

**確定したスコープ:**

1. 保存ファイル名が全部 `master.mp4` に集中する問題を直す（Q2 で方式を決める）
2. Referer を渡せるようにする（Q3 で方式を決める）
3. 期限切れ URL の案内（Q4 で方式を決める）
4. **ffmpeg 孫プロセスが停止されない既存バグの修正** — `CancelDownload` / `PauseDownload` / `UpdateYtDlp`（`waitProcsDrained`）の 3 経路

**対象外:** ライブ配信の録画（「停止してそこまでを保存」）、ローカル .m3u8 ファイルの読み込み

---

## Q2: 保存ファイル名はどうしますか？

HLS の m3u8 にはタイトルのメタデータがありません。そのため現状は保存名が `master.mp4` / `index.mp4` に集中し、
2 本目以降は [uniqueDest](../../../app.go) によって `master (1).mp4` になって**中身が判別できません**。

A) 現状のまま — m3u8 のファイル名ベース（`master.mp4`）。重複時は `(1)` が付く
B) **【推奨】自動でより区別しやすい名前を組み立てる** — URL から取れる情報だけで組む。
   例: `https://vod.example.com/hls/ab12cd/master.m3u8` → `vod.example.com_ab12cd_20260921-0915.mp4`
   （ホスト名 + m3u8 の 1 つ上のパス要素 + 日時。パス要素が無意味なら省略）
C) 登録時にユーザーがタイトル（= 保存ファイル名）を入力・編集できるようにする
D) B を既定にしつつ、C でユーザーが上書きもできるようにする
X) Other (please describe after [Answer]: tag below)

[Answer]: B（2026-09-21 ユーザー回答「進めて」= 推奨どおり）

**B を推奨する理由:** C / D は入力欄が必要になるが、**ドラッグ&ドロップ経路では入力する機会がない**
（[design.md](../application-design/design.md)「ドラッグ&ドロップ入力」で、ドロップは「追加」ボタンと同じ経路を
そのまま辿り UI をブロックしないことが要件化されている）。B は URL だけから決まる**純粋関数**なので
TDD / PBT を回しやすく、既存フローに一切触らない。B の名前で足りないと分かってから C を足すのが順序として安い。

---

## Q3: Referer（元ページの URL）の指定方法はどうしますか？

実測で、Referer を要求するサーバは `--referer` を 1 個渡すだけで 403 が解消しました
（[m3u8-feasibility.md](m3u8-feasibility.md) シナリオ 5→6）。渡し方の選択です。

A) **登録時に任意入力** — URL 入力欄の隣に「元ページ URL（任意）」欄を置き、入力があれば `--referer` に渡す
B) **【推奨】m3u8 URL から自動推定** — m3u8 の URL のオリジン（`https://example.com/`）を Referer として送る。
   **ただし適用は「パスが `.m3u8` で終わる URL」に限定する**（下記理由）
C) **エラー後に追加入力** — まず Referer なしで試し、403 になったアイテムに対して「元ページ URL を指定して再試行」できるようにする
D) **今回は実装しない** — 403 のときエラー文言で「元ページの URL が必要な場合がある」と案内するだけ
X) Other (please describe after [Answer]: tag below)

[Answer]: B（m3u8 URL 限定。2026-09-21 ユーザー回答「進めて」= 推奨どおり）

**B を推奨する理由と、m3u8 限定にする理由:**

- 実測で `--referer` 1 個で 403 が解消した（シナリオ 5→6）。入力欄が不要なのでドロップ経路でも等しく効く
- **m3u8 URL に限定するのはデグレ防止のため。** 既存の 1000 以上の対応サイトは今 Referer なしで動いている。
  全 URL に Referer を送り始めると、それらの経路の挙動を変えてしまう。パスが `.m3u8` で終わる URL だけに
  限れば、**既存サイトの経路には一切触らない**（[CLAUDE.md](../../../CLAUDE.md)「デグレ防止」）
- 判定（`.m3u8` 終わりか / オリジンの抽出）はどちらも純粋関数で、TDD / PBT を回せる
- 視聴ページが別ドメインのサイトでは B は効かない。そのときは C を後から足す

---

## Q4: 期限切れ URL への対応はどうしますか？

m3u8 の URL は有効期限トークン付きのことが多く、失効すると 403 になります。**技術的な対策は存在せず**、
ユーザーが元ページから URL を取り直すしかありません。問題は本アプリの既定が
**同時ダウンロード数 0（登録のみモード）**で、URL をキューに寝かせる運用が既定であるため、
**寝かせている間に失効する**ことです。

A) **登録時に警告** — m3u8 の URL を登録したとき、同時ダウンロード数が 0 なら「m3u8 は期限切れになることがあるため、すぐ開始することを推奨」と伝える
B) **m3u8 は自動で即開始** — m3u8 URL に限り、同時ダウンロード数 0 でも登録と同時にダウンロードを始める（既存の「0 = 自動では始めない」ルールの例外を作る）
C) **エラー文言の改善のみ** — 403 で失敗したとき「URL が失効した可能性があります。元ページから取り直してください」と表示する
D) **【推奨】A と C の両方**
X) Other (please describe after [Answer]: tag below)

[Answer]: D（2026-09-21 ユーザー回答「進めて」= 推奨どおり）

**D を推奨する理由 / B を避ける理由:**

- 失効は防げないので、できるのは「事前に伝える（A）」と「起きたときに正しく説明する（C）」の 2 つだけ。
  両方入れて初めてユーザーが自力で復旧できる
- **B は入れてはいけない。** [design.md](../application-design/design.md)「maxActive == 0（登録のみモード）」は
  「0 のための特別分岐を追加してはいけない。一般ルールがそのまま 0 を包含しており、分岐を足すと
  二重の真実源になる」と明記している。m3u8 のための例外を作ると、そこで決めたルールを壊す

---

## Q5: Security Extensions

Should security extension rules be enforced for this project?
（このプロジェクトでセキュリティ拡張ルールを強制しますか？）

参考: 今回は Referer という**外部から与えられる文字列を新たに yt-dlp へ渡す**設計になるため、
[design.md](../application-design/design.md)「引数インジェクション対策」と同種の検証が必要になります。

A) **【推奨】Yes** — enforce all SECURITY rules as blocking constraints (recommended for production-grade applications)
B) No — skip all SECURITY rules (suitable for PoCs, prototypes, and experimental projects)
X) Other (please describe after [Answer]: tag below)

[Answer]: A（2026-09-21 ユーザー回答「進めて」= 推奨どおり）

**A を推奨する理由（前回は「スキップ」でしたが、今回は事情が違います）:**

- Referer として **新しい文字列を yt-dlp のコマンドラインに渡す**ことになる。これは
  [design.md](../application-design/design.md)「引数インジェクション対策（`--` 終端は必須）」が
  扱っているのと同じ攻撃面で、`--referer` の**値**が `-` 始まりに化ける経路を新たに作る
- Q1 = B により、**プロセスの停止方法そのものを変更する**（プロセスグループ / Job Object）。
  停止対象の指定を誤れば無関係なプロセスを殺しうる

---

## Q6: Property-Based Testing Extension

Should property-based testing (PBT) rules be enforced for this project?
（このプロジェクトでプロパティベーステスト（PBT）ルールを強制しますか？）

参考: 前回（yt-dlp 更新）は「実行」を選択し、`pgregory.net/rapid` で 16 件のプロパティが既に存在します。

A) **【推奨】Yes** — enforce all PBT rules as blocking constraints (recommended for projects with business logic, data transformations, serialization, or stateful components)
B) Partial — enforce PBT rules only for pure functions and serialization round-trips (suitable for projects with limited algorithmic complexity)
C) No — skip all PBT rules (suitable for simple CRUD applications, UI-only projects, or thin integration layers with no significant business logic)
X) Other (please describe after [Answer]: tag below)

[Answer]: A（2026-09-21 ユーザー回答「進めて」= 推奨どおり）

**A を推奨する理由:** 前回と同じ判断で一貫する（`pgregory.net/rapid` で 16 件のプロパティが既に稼働中）。
かつ今回は PBT 向きの純粋関数が 3 つ増える — 保存名の組み立て（Q2 の B）、`.m3u8` 判定とオリジン抽出（Q3 の B）、
停止対象の決定（Q1 の B）。いずれも「任意の URL 文字列」を入力に取るため、
ランダム生成した URL に対する不変条件（出力が常に妥当なファイル名になる / 常に `-` 始まりにならない）を検証する価値が高い。
