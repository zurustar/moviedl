# Contributing

moviedl の開発環境セットアップとビルド方法、リポジトリ構成のメモです。

## 必要なもの

- Go 1.22 以上
- [Wails v2](https://wails.io) (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`)
- macOS / Linux の場合: Xcode Command Line Tools / WebKitGTK
- ffmpeg（ローカル開発時。リリースビルドではバイナリに同梱される）

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

```
moviedl/
├── app.go              バックエンドのメイン: ダウンロード処理、IPC メソッド
├── main.go             Wails 起動エントリ
├── embed.go            embedded/ ディレクトリの埋め込み宣言
├── sysproc_*.go        プロセスサスペンド・コンソール非表示のプラットフォーム別実装
├── frontend/index.html 単一ファイルのフロントエンド（フレームワーク・ビルドステップなし）
├── embedded/           リリースビルド時に ffmpeg バイナリが配置される（gitignore 対象）
├── *_test.go           ユニットテスト（純粋関数中心）
├── Makefile            build / check / fmt / install-hooks などのタスク
├── .githooks/pre-push  push 前に make check を走らせるフック（install-hooks で有効化）
├── docs/
│   ├── requirements.md 要件定義（ユーザー視点での仕様）
│   └── design.md       設計書（実装上の意思決定とピットフォール）
└── .github/workflows/
    ├── ci.yml          push / PR で make check を実行する CI
    └── release.yml     v* タグ push でビルドする CI
```

## アーキテクチャ概要

[Wails v2](https://wails.io) ベースのデスクトップアプリです。Go バイナリが WebView ウィンドウ（macOS: WebKit / Windows: WebView2）を内包し、フロントエンドと Go バックエンドが IPC で通信します。Node.js / npm は使いません。

- **JS → Go**: `App` 構造体の公開メソッドが `window.go.main.App.MethodName()` として呼べる（Promise を返す）
- **Go → JS**: `wailsruntime.EventsEmit(ctx, "download:update", payload)` で発火、JS 側は `window.runtime.EventsOn("download:update", cb)` で購読

詳しい設計判断・ファイル名処理・状態遷移などは [docs/design.md](docs/design.md) を参照してください。

## リリース

`v*` タグを push すると `.github/workflows/release.yml` が起動し、以下の 2 ジョブが並列で走ります。

| ジョブ | ランナー | 成果物 |
|---|---|---|
| `build-macos` | `macos-latest` | `moviedl-macos.zip`（universal: arm64 + amd64） |
| `build-windows` | `windows-latest` | `moviedl-windows.zip` |

CI が `embedded/` にプラットフォーム固有の ffmpeg バイナリを配置してから `wails build` を実行するため、ffmpeg はバイナリに同梱されます。完成した zip は `release` ジョブが GitHub Release にアップロードします。
