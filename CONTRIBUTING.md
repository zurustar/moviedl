# Contributing

moviedl の開発環境セットアップとビルド方法、リポジトリ構成のメモです。

## 必要なもの

- Go 1.25 以上
- [Wails v2](https://wails.io) (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`)
- macOS / Linux の場合: Xcode Command Line Tools / WebKitGTK
- ffmpeg（ローカル開発時。macOS は Homebrew で入れる。リリースビルドには同梱しない）

## ビルド

```bash
# 開発ビルド（macOS, 現在のアーキテクチャ）
~/go/bin/wails build

# macOS ユニバーサルバイナリ（arm64 + amd64）
~/go/bin/wails build -platform darwin/universal

# Windows クロスビルド（Windows ランナー上で実行）
~/go/bin/wails build -platform windows/amd64

# 開発モード（ホットリロード付き）
~/go/bin/wails dev
```

`wails build` が `frontend/wailsjs/` のバインディングを自動再生成します。手動編集しないでください。
出力は `build/bin/` に置かれます。

`make build` はバージョン情報（`git describe` ＋ ビルド日）を `-ldflags` で埋め込みます。`wails build` を直接叩くと `version=dev` になります。リリース時は CI が git タグを注入します（[aidlc-docs/inception/application-design/design.md](aidlc-docs/inception/application-design/design.md)「バージョン情報の埋め込み」を参照）。

## チェック（PR を出す前に必須）

PR を出す前にローカルで以下を必ず通すこと。CI（後述）でも同じチェックが走るが、**手元で先に通すのが原則**。

```bash
make check        # gofmt 検査 + go vet + staticcheck + go test を一括実行
make fmt          # gofmt -w でフォーマットを自動修正
```

`make check` の内訳:

| ステップ | 内容 | 落ちる例 |
|---|---|---|
| gofmt | 未整形ファイルがあれば失敗（`make fmt` で修正） | インデント・整形崩れ |
| `go vet ./...` | 標準の静的解析 | `Printf` の書式ミス等 |
| `staticcheck ./...` | より広い静的解析（`go run` で都度取得） | 未使用関数（`U1000`）= デッドコード検出 |
| `go test ./...` | 全ユニットテスト | ロジックの回帰 |

### コミット前/プッシュ前フック

`make check` を **push 前に自動実行**する git フックを同梱している。各自一度だけ有効化する:

```bash
make install-hooks   # git config core.hooksPath .githooks を設定
```

フック実体は [.githooks/pre-push](.githooks/pre-push)（バージョン管理対象）。チェックが落ちると push が中断される。緊急時のみ `git push --no-verify` で回避できるが、原則使わないこと。

## リポジトリ構成

Go のコードはすべて `package main` です（Wails v2 の標準構成）。パッケージは分けず、
**役割ごとにファイルを分けています**。テストは対応するソースと同じ名前の `*_test.go` に置きます。

```
moviedl/
├── main.go             Wails 起動エントリ（バージョン情報の埋め込みを含む）
├── app.go              App 本体: 状態・scheduler・フロントエンドへの通知・アプリ情報
├── queue.go            URL の登録（重複・不正 URL の拒否理由を返す）と、開始・一時停止・再開・キャンセル・リトライ
├── download.go         1 件のダウンロードの実行（runDownload）と yt-dlp 引数の組み立て
├── progress.go         yt-dlp の進捗行の解析と、経過時間・長さの表示整形
├── urls.go             URL の検証、m3u8 判定、Referer の決定（引数インジェクション対策を含む）
├── filename.go         保存ファイル名の決定（タイトル・m3u8 の生成名・安全化・重複回避）
├── workdir.go          ダウンロードごとの作業ディレクトリの記録と、起動時の残骸掃除
├── logging.go          moviedl.log への記録（セッション管理・トークンのマスク）
├── procs.go            yt-dlp プロセスの登録簿と、孫プロセスまで及ぶ停止
├── sysproc_other.go    プロセス停止の macOS / Linux 実装（プロセスグループ）
├── sysproc_windows.go  プロセス停止の Windows 実装（Job Object）とコンソール非表示
├── ytdlp.go            yt-dlp の配置・インストール・更新
├── ffmpeg.go           ffmpeg の探索と、Windows 向けのアプリ内インストール
├── checksum.go         取得したバイナリの SHA256 照合（yt-dlp / ffmpeg 共通）
├── *_test.go           例示ベーステスト（ソースと同名で対応）
├── pbt_test.go         プロパティベーステスト（pgregory.net/rapid。例示ベースと分離）
├── frontend/
│   ├── index.html      単一ファイルのフロントエンド（フレームワーク・ビルドステップなし）
│   └── wailsjs/        Wails が生成する IPC バインディング（index.html は直接使っていない）
├── build/              Wails のビルド設定（アイコン・Info.plist）。build/bin/ は gitignore 対象
├── Makefile            build / check / fmt / install-hooks などのタスク
├── .githooks/pre-push  push 前に make check を走らせるフック（install-hooks で有効化）
├── .aidlc-rule-details/  AI-DLC ワークフローのルール詳細（CLAUDE.md 第 2 部が参照）
├── aidlc-docs/         ドキュメント（AI-DLC 構造）。一覧は aidlc-docs/README.md
│   ├── inception/requirements/requirements.md  要件定義（ユーザー視点での仕様）
│   └── inception/application-design/design.md  設計書（実装上の意思決定とピットフォール）
└── .github/workflows/
    ├── ci.yml          push / PR で make check を実行する CI
    └── release.yml     v* タグ push でビルドする CI
```

ffmpeg はバイナリに同梱しません（以前は Windows 版に埋め込んでいましたが、ウイルス対策ソフトの誤検知を招くためやめました。
経緯は design.md「なぜ Windows で埋め込みをやめたか」）。

## アーキテクチャ概要

[Wails v2](https://wails.io) ベースのデスクトップアプリです。Go バイナリが WebView ウィンドウ（macOS: WebKit / Windows: WebView2）を内包し、フロントエンドと Go バックエンドが IPC で通信します。Node.js / npm は使いません。

- **JS → Go**: `App` 構造体の公開メソッドが `window.go.main.App.MethodName()` として呼べる（Promise を返す）
- **Go → JS**: `wailsruntime.EventsEmit(ctx, "download:update", payload)` で発火、JS 側は `window.runtime.EventsOn("download:update", cb)` で購読

詳しい設計判断・ファイル名処理・状態遷移などは [aidlc-docs/inception/application-design/design.md](aidlc-docs/inception/application-design/design.md) を参照してください。

## リリース

`v*` タグを push すると `.github/workflows/release.yml` が起動し、以下の 2 ジョブが並列で走ります。

| ジョブ | ランナー | 成果物 |
|---|---|---|
| `build-macos` | `macos-latest` | `moviedl-macos.zip`（arm64。Intel Mac 向けのユニバーサル版はローカルの `make build-universal` で作る） |
| `build-windows` | `windows-latest` | `moviedl-windows.zip` |

どちらも `wails build` をそのまま実行します。ffmpeg はバイナリに同梱しません（Windows はアプリ初回起動後に `InstallFfmpeg` で取得、macOS は Homebrew を案内）。完成した zip は `release` ジョブが GitHub Release にアップロードします。
