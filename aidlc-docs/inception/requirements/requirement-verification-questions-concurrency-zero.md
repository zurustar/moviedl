# 要件確認質問 — 同時ダウンロード数 0 の許容

**対象要件（確定済み）**: 同時ダウンロード数のプルダウンに `0` を追加する。`0` のときは URL の登録（キューへの追加）だけを許容し、ダウンロードは行わない。

**回答方法**: 各質問の `[Answer]:` の後に記号（A/B/C/X）を書いてください。チャットで「Q1: A、Q2: B ...」と返答いただく形でも構いません。

---

## Q1: 0 に切り替えた時点で「実行中」のアイテムはどう扱いますか？

現状の自動補充（scheduler）は「実行中の件数が上限を下回ったら待ちキューから補充する」動作で、上限を下げても実行中のダウンロードを止める処理はありません。

A) そのまま継続する — 新規の自動開始だけが止まる（実装が最も単純／既存挙動の自然な延長）
B) 実行中のアイテムを全て一時停止中リストへ移動する — 0 は「今はダウンロードしない」状態を厳密に表す
C) 実行中のアイテムを待ちキューへ戻す（進捗は破棄）
X) Other (please describe after [Answer]: tag below)

[Answer]:当然Aです。もともとダウンロード中の数を減らしても実行中のダウンロードは停止しない実装になっているはずです。

---

## Q2: 0 のとき、手動開始（待ちキュー／一時停止中アイテムの ▶ 開始・再開）は許可しますか？

既存要件では手動開始は同時ダウンロード数の上限の対象外で、上限を超えて開始できます（[requirements.md](requirements.md) 「手動開始」節）。

A) 許可する — 0 は「自動補充をしない」という意味に限定し、ユーザーが明示的に押した開始・再開は動く（既存要件との整合が取れる）
B) 禁止する — 0 のときはボタンを無効化し、いかなるダウンロードも開始できない（「登録だけ」を厳密に守る）
X) Other (please describe after [Answer]: tag below)

[Answer]:A 当然許可します。これも今と同じです。手動開始で今の最大同時ダウンロード数を超えても実行することになっています。

---

## Q3: 既定値と永続化はどうしますか？

現状は起動時に必ず 1（`NewApp` で `maxActive: 1`）で、設定はアプリ終了時に保存されません。

A) 現状維持 — 既定は 1、永続化なし（起動するたびに 1 に戻る）
B) 既定は 1 のままだが、選択値をアプリ再起動後も保持する（設定の永続化を新規実装）
C) 既定を 0 にする（起動直後はダウンロードを開始しない）
X) Other (please describe after [Answer]: tag below)

[Answer]:C

---

## Q4: Reverse Engineering ステージを実行しますか？

AI-DLC の正規パス `aidlc-docs/inception/reverse-engineering/` の成果物はありませんが、[requirements.md](requirements.md) と [design.md](../application-design/design.md) にアーキテクチャ・自動補充ルール・既知のピットフォールが既に文書化されています。

A) スキップする — 既存の requirements.md / design.md を Reverse Engineering 成果物の代替として使う（今回の変更は小規模なため推奨）
B) 実行する — コードベース全体を解析して reverse-engineering/ 配下の成果物を新規生成する（時間と出力量が増える）
X) Other (please describe after [Answer]: tag below)

[Answer]:今回の要件を既存の要件に追加してください。ゼロが増えるだけで基本的な要件は変わっていないので影響は少ないはずです。

---

## Q5: Security Extensions

Should security extension rules be enforced for this project?
（このプロジェクトでセキュリティ拡張ルールを強制しますか？）

A) Yes — enforce all SECURITY rules as blocking constraints (recommended for production-grade applications)
B) No — skip all SECURITY rules (suitable for PoCs, prototypes, and experimental projects)
X) Other (please describe after [Answer]: tag below)

[Answer]:今回はスキップ

---

## Q6: Property-Based Testing Extension

Should property-based testing (PBT) rules be enforced for this project?
（このプロジェクトでプロパティベーステスト（PBT）ルールを強制しますか？）

A) Yes — enforce all PBT rules as blocking constraints (recommended for projects with business logic, data transformations, serialization, or stateful components)
B) Partial — enforce PBT rules only for pure functions and serialization round-trips (suitable for projects with limited algorithmic complexity)
C) No — skip all PBT rules (suitable for simple CRUD applications, UI-only projects, or thin integration layers with no significant business logic)
X) Other (please describe after [Answer]: tag below)

[Answer]:今回は実行


---
