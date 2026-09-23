# AI-DLC State Tracking

## Project Information
- **Project Type**: Brownfield
- **Start Date**: 2026-08-06T00:00:00Z
- **Current Stage**: 作業 6「リポジトリ構成の整理」完了（動作変更なし）。次の要件の待機中
- **最新リリース**: v0.2.4（commit e533c2d）

## Workspace State
- **Existing Code**: Yes
- **Programming Languages**: Go（`package main` のみ。役割ごとにファイルを分割。一覧は [CONTRIBUTING.md](../CONTRIBUTING.md)「リポジトリ構成」）、JavaScript + HTML（`frontend/index.html`）
- **Build System**: Wails v2（`wails.json`）+ Go modules + Makefile
- **Project Structure**: 単一デスクトップアプリ（Monolith）
- **Workspace Root**: /Users/oumi/Documents/GitHub/moviedl
- **Reverse Engineering Needed**: No — 要件 1 でユーザー判断によりスキップ（Q4）。既存の `requirements.md` / `application-design/design.md` を成果物の代替とし、以後の要件もこれらへ追記する

## Code Location Rules
- **Application Code**: Workspace root (NEVER in aidlc-docs/)
- **Documentation**: aidlc-docs/ only
- **Structure patterns**: See code-generation.md Critical Rules

## ドキュメントの命名規則

AI-DLC のフォルダ構成（`inception/` / `construction/`）はそのまま使い、**要件ごとの成果物はファイル名の末尾に要件の略称を付ける**
（例: `code-generation-plan-m3u8.md`）。要件と成果物の対応は [README.md](README.md) を参照。
`requirements.md` と `design.md` は要件ごとに分けず 1 ファイルに追記する（既存方針）。

## 要件の一覧

| # | 要件 | 略称 | 状態 | リリース | Security | PBT |
|---|---|---|---|---|---|---|
| 1 | 同時ダウンロード数 0（登録のみモード） | `concurrency-zero` | 完了 | v0.2.1（35c8767） | No | Yes / Full |
| 2 | yt-dlp の手動更新 | `ytdlp-update` | 完了 | v0.2.2（588e13f） | No | Yes / Full |
| 3 | m3u8（HLS）対応 + ffmpeg 孫プロセスの停止漏れ修正 | `m3u8` | 完了（**実機確認は未実施**） | v0.2.3（7f89c82） | **Yes** / Full | Yes / Full |
| 4・5 | 登録が拒否された理由の表示 + 元ページ URL を指定して再試行 | `diagnostics-referer` | 完了（**実機確認は未実施**） | v0.2.4（e533c2d） | Yes / Full | Yes / Full |
| 作業 6 | リポジトリ構成の整理（動作変更なし） | — | 完了 | —（リリース不要） | Yes / Full | Yes / Full |

## Extension Configuration

| Extension | Enabled | Enforcement Mode | Decided At |
|---|---|---|---|
| Security Baseline | **Yes** | Full（全 SECURITY ルールを blocking として強制） | Requirements Analysis（要件 3・Q5 = A）。以後継続 |
| Property-Based Testing | Yes | Full（全 PBT ルールを blocking として強制） | Requirements Analysis（要件 1）。以後継続 |

- 両方 opt-in のため `security-baseline.md` と `property-based-testing.md` をロードする
- Security Baseline は要件 1・2 では No。要件 3 で Yes に変更した理由: (a) Referer という新しい文字列を yt-dlp の
  コマンドラインに渡す（引数インジェクション面の拡大）、(b) プロセスの停止方法そのものを変更する
- Go 用 PBT フレームワークは **`pgregory.net/rapid` v1.3.0**（PBT-09。`go.mod` に追加済み、design.md「テスト可能プロパティ（PBT-01）」に記録）

## Stage Progress

各要件で実行・スキップしたステージ。スキップの詳しい理由は各要件の実行計画（workflow-plan）に書いてある。

| ステージ | 要件 1 | 要件 2 | 要件 3 | 要件 4・5 |
|---|---|---|---|---|
| Workspace Detection | ✅ | ✅ | ✅ | ✅ |
| Reverse Engineering | ⏭️ | ⏭️ | ⏭️ | ⏭️ |
| Requirements Analysis | ✅ | ✅（質問は対話で実施。audit.md に記録） | ✅（実現可能性評価 + Q1〜Q6） | ✅ |
| User Stories | ⏭️ | ⏭️ | ⏭️ | ⏭️ |
| Workflow Planning | ✅ | —（計画書なし） | ✅ | —（計画書なし） |
| Application Design | ✅ minimal | ✅ | ✅ standard | ✅ |
| Units Generation | ⏭️（単一ユニット） | ⏭️ | ⏭️ | ⏭️ |
| Functional Design | ⏭️（Application Design に統合） | ⏭️ | ⏭️ | ⏭️ |
| NFR Requirements | ✅（PBT-09 のみ） | ⏭️ | ✅ minimal | ⏭️ |
| NFR Design / Infrastructure Design | ⏭️ | ⏭️ | ⏭️ | ⏭️ |
| Code Generation | ✅ TDD 5 サイクル | ✅ TDD 6 サイクル | ✅ TDD 13 サイクル | ✅ TDD 6 サイクル |
| Build and Test | ✅ | ✅（結果は実装計画に記載） | ✅ 67 テスト | ✅ 81 テスト（結果は実装計画に記載） |

要件 2 と要件 4・5 は実行計画書を作らずに進めた（前者は対話で方式を決め、後者はユーザーが提案を承認したため）。
当時の判断は audit.md に残っている。

## 未完了の事項

- [ ] **要件 3 の macOS 実機確認** — m3u8 のダウンロード・保存名・キャンセルで ffmpeg が残らないこと・ログのマスク・既存サイトのデグレ確認
- [ ] **要件 3 の Windows 実機確認** — Job Object によるプロセスツリー停止は開発機（macOS）で検証できない。確認できたのはクロスビルドと `go vet` のみで、**「孫プロセスまで停止が及ぶ」という主目的が Windows で達成されているかは未検証**
- [ ] **要件 4・5 の実機確認** — チェックリストは [code-generation-plan-diagnostics-referer.md](construction/plans/code-generation-plan-diagnostics-referer.md)

## 対象外と判定した事項（要件 3〜5 で検討）

- **ページ URL から JavaScript 実行後の m3u8 を発見すること。** yt-dlp は JS を実行しない（実測: m3u8 の URL が HTML に
  文字列として存在すれば `html5` エクストラクタが発見するが、JS が組み立てる場合は `Unsupported URL`）。解決には
  ヘッドレスブラウザが必要で、design.md の「WebView にリモートコンテンツを読み込まない」規約（`AddToQueue` 等の
  ファイル書き込み API がバインドされているため）に正面から反する。実装するなら隔離した別プロセスが必要
- ライブ配信の録画（停止時の出力が 0 バイトになることを実測）、ローカル .m3u8 の読み込み、DRM

## 作業 6: リポジトリ構成の整理

**経緯:** ユーザーから「ディレクトリ構成が汚くないか」と指摘を受けた（2026-09-23）。調べたところ、Wails の標準である
「Go ファイルをルートに直置き」は普通の形だったが、次の 2 点は本物の問題だった。

1. `app.go` が 2065 行あり、ダウンロード・yt-dlp・ffmpeg・プロセス停止・ログ・ファイル名・URL 判定が 1 ファイルに入っていた
2. `aidlc-docs/` の成果物の名前が要件ごとにバラバラだった（要件 1 だけ接尾辞なし、など）

**実施内容:**
- [x] `app.go` を役割ごとに 12 ファイルへ分割（同じ `package main` のまま）。**宣言 114 件の本文が分割前後で 1 文字も変わっていない**ことを構文解析で照合
- [x] `app_test.go` / `helpers_test.go` をソースに対応する 12 個の `*_test.go` へ分割。**宣言 44 件の本文が同一**、テスト一覧（81 件）も分割前と完全一致
- [x] `aidlc-docs/` のファイル名を「要件の略称を末尾に付ける」規則にそろえた（6 ファイルを `git mv`）
- [x] 改名したファイル・移動した関数を指すリンクを張り替えた（作業記録の本文中の旧ファイル名は、当時の記録として書き換えない）
- [x] CONTRIBUTING.md の構成図を更新。あわせて以前から古くなっていた記述（存在しない `embed.go` / `embedded/`、「リリースで ffmpeg を同梱」、「macOS 版は universal」）を実態に合わせた
- [x] 要件 1 の PBT 適合表を、状態ファイルから要件 1 のテスト結果ファイルへ移した
- [x] `aidlc-docs/README.md`（要件と成果物の対応表）を追加
- [x] `make check` / Windows クロスビルドの最終確認（81 テスト通過・テスト一覧が整理前と完全一致）
- [x] コミット（コード分割 defab10 と、ドキュメント整理の 2 つに分けた）
