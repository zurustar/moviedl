// app.go: App 本体（状態・scheduler・フロントエンドへの通知）とアプリ情報。

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx       context.Context
	mu        sync.Mutex
	items     []*DownloadItem
	schedCh   chan struct{}
	maxActive int // 自動補充で維持する実行中アイテム数の上限（0〜10、既定 0 = 登録のみモード）

	// procs は生存している yt-dlp プロセスの登録簿。yt-dlp を起こす「すべての」
	// exec.Command が registerProc / unregisterProc を通る必要がある。
	// 1 箇所でも漏れると UpdateYtDlp がそのプロセスを止められない。
	// design.md「停止対象は『実行中』だけでは足りない」参照。
	procs map[*exec.Cmd]struct{}
	// updating が true の間は yt-dlp の新規起動を拒否する（実行ファイル置き換え中の起動を防ぐ）。
	updating bool
}

type DownloadItem struct {
	ID        string  `json:"id"`
	URL       string  `json:"url"`
	Title     string  `json:"title"`
	Percent   float64 `json:"percent"`
	Speed     string  `json:"speed"`
	TotalSize string  `json:"totalSize"`
	ETA       string  `json:"eta"`
	Elapsed   string  `json:"elapsed"`
	Status    string  `json:"status"` // "queued"|"downloading"|"paused"|"finished"|"error"|"cancelled"
	Error     string  `json:"error,omitempty"`
	// Referer はユーザーが指定した元ページ URL（未指定なら空）。
	// 自動導出（m3u8 のオリジン）が効かない構成のための逃げ道。
	// **リトライ・再キューで消してはいけない**（消えたら再試行の意味がなくなる）。
	Referer string `json:"referer,omitempty"`

	outputDir  string
	cmd        *exec.Cmd
	startedAt  time.Time
	cancelFlag int32 // atomic: 1 = cancelled
	// stopFlag は yt-dlp 更新のために停止されたことを表す（atomic: 1 = 更新のため停止）。
	// これがないと Kill された yt-dlp の Wait エラーが "error" 扱いになってしまう。
	stopFlag int32
}

func (item *DownloadItem) markCancelled() { atomic.StoreInt32(&item.cancelFlag, 1) }

func (item *DownloadItem) isCancelled() bool {
	return atomic.LoadInt32(&item.cancelFlag) == 1
}

func (item *DownloadItem) markStoppedForUpdate() { atomic.StoreInt32(&item.stopFlag, 1) }

func (item *DownloadItem) clearStoppedForUpdate() {
	atomic.StoreInt32(&item.stopFlag, 0)
}

func (item *DownloadItem) isStoppedForUpdate() bool {
	return atomic.LoadInt32(&item.stopFlag) == 1
}

var dlCounter int64

func NewApp() *App {
	return &App{
		schedCh: make(chan struct{}, 1),
		// 既定は 0（登録のみモード）。起動直後に前回分が勝手に走り出さないようにする。
		// design.md「maxActive == 0（登録のみモード）」参照。1 に戻してはいけない。
		maxActive: 0,
		procs:     make(map[*exec.Cmd]struct{}),
	}
}

// SetMaxConcurrent は自動補充で維持する実行中アイテム数の上限を設定する（0〜10 にクランプ）。
// 0 は「自動補充を一切行わない（登録のみモード）」を意味する。
// 設定後に scheduler を起こし、引き上げ時は待ちキューから即座に補充させる。
// 引き下げ（0 への切り替えを含む）で実行中のアイテムを停止させることはない。
func (a *App) SetMaxConcurrent(n int) {
	if n < 0 {
		n = 0
	}
	if n > 10 {
		n = 10
	}
	a.mu.Lock()
	a.maxActive = n
	a.mu.Unlock()
	a.notify()
}

// GetMaxConcurrent は現在の同時ダウンロード数上限を返す（フロントエンドの初期値用）。
func (a *App) GetMaxConcurrent() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.maxActive
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	startLogSession()
	cleanupLeftoverWorkDirs()
	go a.scheduler()
}

func (a *App) removeItem(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, it := range a.items {
		if it.ID == id {
			a.items = append(a.items[:i], a.items[i+1:]...)
			return
		}
	}
}

// notify sends a scheduling signal (non-blocking; duplicates are coalesced).
func (a *App) notify() {
	select {
	case a.schedCh <- struct{}{}:
	default:
	}
}

// scheduler auto-starts queued items until the active count reaches maxActive.
func (a *App) scheduler() {
	for range a.schedCh {
		a.mu.Lock()
		toStart := selectToStart(a.items, a.maxActive)
		for _, it := range toStart {
			it.Status = "downloading"
		}
		a.mu.Unlock()

		// ロックを解放してから起動する（runDownload も mu を取るため）。
		for _, it := range toStart {
			a.emit(it)
			go a.runDownload(it)
		}
	}
}

// selectToStart は items のうち、実行中件数が maxActive に達するまで先頭から
// 起動すべき "queued" アイテムを返す。状態は変更しない（呼び出し側の責務）。
// aidlc-docs/inception/application-design/design.md「自動補充ルール（scheduler）」を参照。
func selectToStart(items []*DownloadItem, maxActive int) []*DownloadItem {
	active := 0
	for _, it := range items {
		if it.Status == "downloading" {
			active++
		}
	}
	var out []*DownloadItem
	for _, it := range items {
		if active >= maxActive {
			break
		}
		if it.Status == "queued" {
			out = append(out, it)
			active++
		}
	}
	return out
}

func (a *App) emit(item *DownloadItem) {
	if !item.startedAt.IsZero() && item.Status == "downloading" {
		item.Elapsed = formatElapsed(time.Since(item.startedAt))
	}
	// ctx は startup で設定される。未設定なのは startup 前（= 単体テスト）だけで、
	// その状態では通知先のフロントエンドが存在しない。Wails の EventsEmit は
	// nil ctx でエラーを吐くため、ここで止めて App のメソッドを単体テストできるようにする。
	if a.ctx == nil {
		return
	}
	wailsruntime.EventsEmit(a.ctx, "download:update", *item)
}

// AppVersion はフロントエンド表示用のバージョン文字列を返す。
func (a *App) AppVersion() string { return formatVersion(version, buildDate) }

// formatVersion はバージョン表示文字列を組み立てる。
// リリース（タグ注入済み）はタグをそのまま、dev のときだけビルド日を併記する。
func formatVersion(version, buildDate string) string {
	if version == "" {
		version = "dev"
	}
	if version == "dev" && buildDate != "" {
		return version + " (" + buildDate + ")"
	}
	return version
}

func (a *App) GetDefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, "Downloads")
}

func (a *App) SelectDirectory() string {
	dir, err := wailsruntime.OpenDirectoryDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Title:            "保存先フォルダを選択",
		DefaultDirectory: a.GetDefaultDir(),
	})
	if err != nil || dir == "" {
		return ""
	}
	return dir
}
