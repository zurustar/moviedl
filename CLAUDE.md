# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build

```bash
# Development build (macOS, current arch)
~/go/bin/wails build

# Production universal binary for macOS (arm64 + amd64)
~/go/bin/wails build -platform darwin/universal

# Windows cross-compile (run on Windows runner)
~/go/bin/wails build -platform windows/amd64

# Dev mode with hot-reload (frontend changes reflect immediately)
~/go/bin/wails dev
```

`wails build` regenerates `frontend/wailsjs/` bindings automatically — do not edit those files by hand.

Output lands in `build/bin/`.

## Architecture

This is a [Wails v2](https://wails.io) app: a Go binary that embeds a WebKit/WebView2 window serving a local HTML page. There is no Node.js or npm involved.

### Go backend (`app.go`, `main.go`, `embed.go`)

- `App` struct is bound to the frontend via Wails IPC. Any exported method becomes callable as `window.go.main.App.MethodName()` in JS.
- `StartDownload(url, outputDir string) string` spawns a goroutine, runs `yt-dlp` as a subprocess, streams its stdout/stderr through `os.Pipe`, and emits `download:progress` events to the frontend via `wailsruntime.EventsEmit`.
- yt-dlp and ffmpeg are stored in `os.UserConfigDir()/moviedl/`. yt-dlp is downloaded at runtime via `InstallYtDlp()`; ffmpeg is extracted from the embedded binary on startup via `extractEmbeddedFfmpeg()`.
- When ffmpeg is available, yt-dlp is invoked with `-f bestvideo+bestaudio/best --merge-output-format mp4`. Without ffmpeg it falls back to `-f best[ext=mp4]/best`.

### Frontend (`frontend/index.html`)

Single self-contained HTML file — no framework, no build step. Communicates with Go via:
- `window.go.main.App.*` — calling Go methods (returns Promises)
- `window.runtime.EventsOn('download:progress', cb)` — receiving Go → JS events

### Embedded assets (`embed.go`, `embedded/`)

`embed.go` embeds `all:embedded` into the binary. In release builds, CI downloads platform-specific ffmpeg binaries into `embedded/` before `wails build` runs, so ffmpeg ships inside the `.app`/`.exe`. The `embedded/ffmpeg` and `embedded/ffmpeg.exe` files are gitignored.

## Download implementation — design decisions and known pitfalls

### ファイル名に `(1)` が付く問題（繰り返し発生）

**絶対にやってはいけないこと:**

1. `-o "%(title)s.%(ext)s"` を使うな。タイトル名で仮ファイルを作ると、outputDir に同名ファイルがある場合に yt-dlp が `(1)` を自動付与する。
2. `-P home:outputDir` を使うな。yt-dlp が最終ファイルを outputDir に直接書こうとするため、outputDir の既存ファイルと競合すると `(1)` が付く。
3. `uniqueDest` を削除するな。ユーザーの既存ファイルを黙って上書き・破壊する厳禁の変更。`uniqueDest` は outputDir に同名ファイルが**実際に存在する**場合にのみ連番を付与する正当な重複回避機構。
4. タイトル取得に `--print "%(title)s"` を使うな。`--print` は出力テンプレート評価を経るため、yt-dlp が内部で同一 ID を 2 回処理すると `title` に ` (1)` が付加される。

**正しいタイトル取得方法（STEP2a）:**

```
yt-dlp --skip-download --dump-json --no-playlist URL
```

返ってきた JSON の `id` フィールドが `-1` で終わり、かつ `title` が ` (1)` で終わる場合は yt-dlp の内部 dedup アーティファクト。両方マッチした場合のみ ` (1)` を除去する。実際のタイトルに ` (1)` が含まれる場合は `id` が `-1` で終わらないため誤除去は起きない。

**正しいダウンロード設計（STEP2〜5）:**

```
yt-dlp -o "<crypto/rand 8バイト hex>.%(ext)s"   ← 衝突ゼロの仮ファイル名
       -P home:workDir                           ← outputDir を一切見ない
       -P temp:workDir
       bestvideo+bestaudio/best
       --merge-output-format mp4
```

yt-dlp 完了後:
1. STEP2a で取得済みのタイトルを使用（info.json は不要）
2. `workDir/tmpBase.mp4` → `os.Rename` で `uniqueDest(outputDir, title.mp4)` へ移動

### Release (`github/workflows/release.yml`)

Triggered by `v*` tags. Two parallel jobs:
1. `build-macos` — runs on `macos-latest`, downloads arm64+amd64 ffmpeg from `yt-dlp/FFmpeg-Builds`, combines with `lipo` into a universal binary, builds `darwin/universal`, zips the `.app`.
2. `build-windows` — runs on `windows-latest`, downloads `win64-gpl` ffmpeg zip, extracts `ffmpeg.exe`, builds `windows/amd64`.

Both upload artifacts to a `release` job that creates the GitHub Release.
