// queue.go: URL の登録と、アイテムへの操作（開始・一時停止・再開・キャンセル・リトライ）。

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
)

type PlaylistEntry struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	Title     string `json:"title"`
	Thumbnail string `json:"thumbnail"`
	Duration  string `json:"duration"`
}

// FetchPlaylist fetches video entries from a URL.
// Returns a single entry for individual videos, multiple entries for playlists.
func (a *App) FetchPlaylist(rawURL string) ([]PlaylistEntry, error) {
	ytdlp, err := a.ytDlpPath()
	if err != nil {
		return nil, err
	}
	if !isValidURL(rawURL) {
		return nil, fmt.Errorf("不正な URL です（http:// または https:// で始まる必要があります）")
	}
	cmd := exec.Command(ytdlp,
		"--flat-playlist", "--dump-json",
		"--no-warnings",
		"--", rawURL,
	)
	cmd.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONIOENCODING=utf-8")
	applyOSProcAttr(cmd)

	// 登録簿を通す（UpdateYtDlp がこのプロセスを停止できるようにするため）。
	if !a.registerProc(cmd) {
		return nil, fmt.Errorf("yt-dlp を更新中です。完了後に再度お試しください")
	}
	defer a.unregisterProc(cmd)

	// 登録経路はログを一切書いていなかったため、URL が消えたときに原因を追えなかった。
	// 対象 URL・件数・失敗理由を残す。design.md「ログのセッション管理」を参照。
	appendLog("[FETCH] 開始: url=%s", rawURL)

	out, err := cmd.Output()
	if err != nil {
		// cmd.Output() は Stderr 未設定時に ExitError.Stderr へ標準エラーを詰める。
		// yt-dlp の失敗理由（Unsupported URL など）はここにしか出ないので必ず残す。
		var stderr string
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			stderr = strings.TrimSpace(string(ee.Stderr))
		}
		appendLog("[FETCH] 失敗: url=%s err=%v stderr=%q", rawURL, err, stderr)
		return nil, fmt.Errorf("情報取得失敗: %w", err)
	}

	entries, perr := parsePlaylistJSON(out)
	if perr != nil {
		appendLog("[FETCH] 解析失敗: url=%s err=%v out=%d bytes", rawURL, perr, len(out))
		return nil, perr
	}
	appendLog("[FETCH] 成功: url=%s entries=%d", rawURL, len(entries))
	for i, e := range entries {
		appendLog("[FETCH]   entry[%d] url=%s title=%q", i, e.URL, e.Title)
	}
	return entries, nil
}

// parsePlaylistJSON は yt-dlp --dump-json の出力（1 行 1 JSON）を解析する。
// webpage_url を優先し無ければ url を採用、URL 無し行とパース不能行はスキップする。
// 有効エントリが 0 件ならエラーを返す。aidlc-docs/inception/application-design/design.md「プレイリスト・ファイル選択」参照。
func parsePlaylistJSON(out []byte) ([]PlaylistEntry, error) {
	type rawEntry struct {
		ID         string  `json:"id"`
		URL        string  `json:"url"`
		WebpageURL string  `json:"webpage_url"`
		Title      string  `json:"title"`
		Thumbnail  string  `json:"thumbnail"`
		Duration   float64 `json:"duration"`
	}

	var entries []PlaylistEntry
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e rawEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		u := e.WebpageURL
		if u == "" {
			u = e.URL
		}
		if u == "" {
			continue
		}
		entries = append(entries, PlaylistEntry{
			ID:        e.ID,
			URL:       u,
			Title:     e.Title,
			Thumbnail: e.Thumbnail,
			Duration:  formatDuration(int(e.Duration)),
		})
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("動画情報を取得できませんでした")
	}
	return entries, nil
}

// IsM3U8URL はフロントエンドから m3u8 判定を参照するための読み取り専用 API。
//
// フロントエンド側で `.m3u8` の文字列判定を書くと述語が二重化し、片方だけ直したときに
// 挙動が食い違う（クエリ付き URL の扱いを間違えるのが典型）。判定は Go の
// isM3U8URL に一本化する。design.md「判定は 1 つの述語に集約する」を参照。
func (a *App) IsM3U8URL(url string) bool { return isM3U8URL(url) }

// containsURL は items のいずれかが同一 URL（完全一致）を持つかを返す。
// 重複登録防止に使う。aidlc-docs/inception/application-design/design.md
// 「キュー登録（AddToQueue）と重複防止」を参照。
func containsURL(items []*DownloadItem, url string) bool {
	for _, it := range items {
		if it.URL == url {
			return true
		}
	}
	return false
}

// AddResult は AddToQueue の結果。拒否した場合は理由と表示文言を返す。
//
// **空文字だけを返して黙って捨ててはいけない。** 2026-09-23 に、登録が拒否された
// アイテムが「開始もせずリストから消えた」ように見え、画面にもログにも情報が
// 残らずユーザーも開発者も原因を特定できない事象が起きた。
// design.md「拒否した理由を返す（沈黙させない）」を参照。
type AddResult struct {
	ID      string `json:"id"`      // 受理時のみ
	Reason  string `json:"reason"`  // "" = 受理 / "invalid" / "duplicate"
	Message string `json:"message"` // 拒否理由の表示文言（受理時は ""）
}

// addRejection は登録を拒否すべきかとその理由を返す。"" なら受理。
//
// 不正 URL の判定を重複判定より先に行う。不正な値を「重複」と report すると
// ユーザーが誤った対処（既存アイテムの削除）に誘導される。
func addRejection(items []*DownloadItem, url string) string {
	if !isValidURL(url) {
		return "invalid"
	}
	if containsURL(items, url) {
		return "duplicate"
	}
	return ""
}

// addRejectionMessage は拒否理由をユーザー向けの文言にする。
// 未知の理由でも空文字を返さない（沈黙させないことがこの関数の目的）。
func addRejectionMessage(reason string) string {
	switch reason {
	case "":
		return ""
	case "invalid":
		return "この URL は登録できません（http:// または https:// で始まる必要があります）。"
	case "duplicate":
		return "この URL は既に登録されています。エラー状態のものを再実行する場合はリトライを使ってください。"
	default:
		return "この URL は登録できませんでした（理由: " + reason + "）。"
	}
}

// AddToQueue registers a URL as a queued item and notifies the scheduler.
// 拒否した場合は理由と表示文言を返す（呼び出し側が必ずユーザーへ提示する）。
// 判定と append は同一ロック区間で行う
// （Wails の各 IPC は別 goroutine のため、同一 URL の同時登録を防ぐ）。
func (a *App) AddToQueue(url, outputDir string) AddResult {
	a.mu.Lock()
	reason := addRejection(a.items, url)
	if reason != "" {
		a.mu.Unlock()
		appendLog("[ADD] 拒否: reason=%s url=%s", reason, url)
		return AddResult{Reason: reason, Message: addRejectionMessage(reason)}
	}
	id := fmt.Sprintf("%d", atomic.AddInt64(&dlCounter, 1))
	item := &DownloadItem{
		ID:        id,
		URL:       url,
		outputDir: outputDir,
		Status:    "queued",
	}
	a.items = append(a.items, item)
	a.mu.Unlock()

	appendLog("[ADD] 受理: id=%s url=%s", id, url)
	a.emit(item)
	a.notify()
	return AddResult{ID: id}
}

// StartDownload manually moves a queued item to active, bypassing the scheduler's
// "only start when active is empty" rule — enabling parallel downloads.
func (a *App) StartDownload(id string) {
	a.mu.Lock()
	var item *DownloadItem
	for _, it := range a.items {
		if it.ID == id && it.Status == "queued" {
			item = it
			break
		}
	}
	if item != nil {
		item.Status = "downloading"
	}
	a.mu.Unlock()
	if item == nil {
		return
	}
	a.emit(item)
	go a.runDownload(item)
}

// PauseDownload suspends an active download and notifies the scheduler
// so it can auto-start the next queued item if the active list is now empty.
func (a *App) PauseDownload(id string) {
	a.mu.Lock()
	var item *DownloadItem
	for _, it := range a.items {
		if it.ID == id && it.Status == "downloading" {
			item = it
			break
		}
	}
	var cmd *exec.Cmd
	if item != nil {
		item.Status = "paused"
		cmd = item.cmd // runDownload はロック内で item.cmd を書くため、退避もロック内で行う
	}
	a.mu.Unlock()
	if item == nil {
		return
	}
	if cmd != nil {
		// ツリー単位で中断する。yt-dlp だけを SIGSTOP すると ffmpeg が
		// ダウンロードを続け、一時停止したはずが実際には止まらない。
		suspendProcessTree(cmd) //nolint:errcheck
	}
	a.emit(item)
	a.notify()
}

// ResumeDownload resumes a paused download.
func (a *App) ResumeDownload(id string) {
	a.mu.Lock()
	var item *DownloadItem
	for _, it := range a.items {
		if it.ID == id && it.Status == "paused" {
			item = it
			break
		}
	}
	var cmd *exec.Cmd
	if item != nil {
		item.Status = "downloading"
		cmd = item.cmd // ロック内で退避（runDownload がロック内で書くため）
	}
	a.mu.Unlock()
	if item == nil {
		return
	}
	if cmd != nil {
		resumeProcessTree(cmd) //nolint:errcheck
	}
	a.emit(item)
}

func (a *App) CancelDownload(id string) {
	a.mu.Lock()
	var item *DownloadItem
	for _, it := range a.items {
		if it.ID == id {
			item = it
			break
		}
	}
	// cmd と status はロック内で退避する（runDownload がロック内で item.cmd を書き、
	// status も他経路と競合しうるため）。
	var cmd *exec.Cmd
	var status string
	if item != nil {
		cmd = item.cmd
		status = item.Status
	}
	a.mu.Unlock()

	if item == nil {
		return
	}
	item.markCancelled()

	if cmd != nil && cmd.Process != nil {
		// Resume before killing so the process can receive SIGKILL on Unix.
		if status == "paused" {
			resumeProcessTree(cmd) //nolint:errcheck
		}
		// ツリー単位で止める。yt-dlp だけを Kill すると ffmpeg が孤児化して
		// 裏で帯域を食い続け、workDir を RemoveAll しても書き込みが続く。
		killProcessTree(cmd) //nolint:errcheck
	}

	if status == "queued" || status == "paused" || status == "error" {
		a.mu.Lock()
		item.Status = "cancelled"
		a.mu.Unlock()
		a.emit(item)
		a.removeItem(item.ID)
	}
}

// RetryDownload はエラー終了したアイテムを再キューする。
// aidlc-docs/inception/application-design/design.md「リトライ（RetryDownload）」を参照。
func (a *App) RetryDownload(id string) {
	a.mu.Lock()
	var item *DownloadItem
	for _, it := range a.items {
		if it.ID == id && it.Status == "error" {
			item = it
			break
		}
	}
	if item != nil {
		resetForRetry(item)
	}
	a.mu.Unlock()
	if item == nil {
		return
	}
	a.emit(item)
	a.notify()
}

// resetForRetry はエラーアイテムを再実行できる状態に戻す。
// RetryDownload と RetryWithReferer が共有する（初期化漏れを 1 箇所に集める）。
//
// **Referer はクリアしない。** ユーザーが再試行のために指定した値なので、
// ここで消すと指定した意味がなくなる。
// a.mu の保護下で呼ぶこと。
func resetForRetry(item *DownloadItem) {
	item.Status = "queued"
	item.Error = ""
	item.Percent = 0
	item.Speed = ""
	item.ETA = ""
	item.Elapsed = ""
	item.TotalSize = ""
	atomic.StoreInt32(&item.cancelFlag, 0)
}

// RetryWithReferer は元ページ URL を Referer として設定し、エラーアイテムを再キューする。
// 戻り値は "" が成功、非空はユーザーへ提示するエラー文言。
//
// なぜ必要か: 自動導出（refererFor）は m3u8 自身のオリジンを返すため、プレイヤーや CDN が
// 視聴ページと別ドメインにある構成では効かない。その逃げ道として用意する。
// design.md「Referer のユーザー指定（元ページ URL）」を参照。
func (a *App) RetryWithReferer(id, pageURL string) string {
	// 入口で検証する。"-" 始まりの値が --referer の引数に化けるのを防ぐ
	// （design.md「引数インジェクション対策」と同じ多層防御。effectiveReferer が最後の砦）。
	page := strings.TrimSpace(pageURL)
	if !isValidURL(page) {
		return "元ページの URL が不正です（http:// または https:// で始まる必要があります）。"
	}

	a.mu.Lock()
	var item *DownloadItem
	for _, it := range a.items {
		if it.ID == id && it.Status == "error" {
			item = it
			break
		}
	}
	if item != nil {
		item.Referer = page
		resetForRetry(item)
	}
	a.mu.Unlock()

	if item == nil {
		return "再試行できる対象が見つかりません（エラー状態のアイテムのみ指定できます）。"
	}
	appendLog("[RETRY] 元ページ URL を指定して再試行: id=%s referer=%s url=%s", id, page, item.URL)
	a.emit(item)
	a.notify()
	return ""
}
