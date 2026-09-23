# aidlc-docs

AI-DLC（CLAUDE.md 第 2 部）で作成したドキュメントの置き場所です。

## まず読むもの

| ファイル | 内容 |
|---|---|
| [inception/requirements/requirements.md](inception/requirements/requirements.md) | 要件定義。全要件を 1 ファイルに追記している |
| [inception/application-design/design.md](inception/application-design/design.md) | 設計書。実装上の取り決めと**過去に踏んだ罠**。コードを変更する前に必ず読む |
| [aidlc-state.md](aidlc-state.md) | 現在の進捗と、未完了の事項 |

## 要件ごとの成果物

要件ごとの成果物は、ファイル名の末尾に要件の略称を付けています。
`—` は、その要件ではその成果物を作らなかったことを表します（理由は aidlc-state.md の Stage Progress）。

| # | 要件 | 略称 | リリース | 質問と回答 | 実現可能性 | 実行計画 | 実装計画 | テスト結果 |
|---|---|---|---|---|---|---|---|---|
| 1 | 同時ダウンロード数 0（登録のみモード） | `concurrency-zero` | v0.2.1 | [質問](inception/requirements/requirement-verification-questions-concurrency-zero.md) | — | [計画](inception/plans/workflow-plan-concurrency-zero.md) | [実装](construction/plans/code-generation-plan-concurrency-zero.md) | [結果](construction/build-and-test/build-and-test-summary-concurrency-zero.md) |
| 2 | yt-dlp の手動更新 | `ytdlp-update` | v0.2.2 | —（対話で決定） | — | — | [実装](construction/plans/code-generation-plan-ytdlp-update.md) | —（実装計画に記載） |
| 3 | m3u8（HLS）対応 + ffmpeg 孫プロセスの停止漏れ修正 | `m3u8` | v0.2.3 | [質問](inception/requirements/requirement-verification-questions-m3u8.md) | [評価](inception/requirements/feasibility-m3u8.md) | [計画](inception/plans/workflow-plan-m3u8.md) | [実装](construction/plans/code-generation-plan-m3u8.md) | [結果](construction/build-and-test/build-and-test-summary-m3u8.md) |
| 4・5 | 登録が拒否された理由の表示 + 元ページ URL を指定して再試行 | `diagnostics-referer` | v0.2.4 | —（提案を承認） | — | — | [実装](construction/plans/code-generation-plan-diagnostics-referer.md) | —（実装計画に記載） |

## 作業の記録

[audit.md](audit.md) に、ユーザーの入力とその時の判断をすべて時系列で残しています。**追記専用**で、過去の記述は書き換えません。
そのため、2026-09-23 に `app.go` を役割ごとに分割し成果物の名前をそろえる前の記述には、当時のファイル名
（`app.go` / `helpers_test.go` / `code-generation-plan.md` など）がそのまま出てきます。各要件の実装計画・テスト結果の本文も同様です。
