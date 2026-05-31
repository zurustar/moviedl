package main

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
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
	maxActive int // 自動補充で維持する実行中アイテム数の上限（1〜10）
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

	outputDir  string
	cmd        *exec.Cmd
	startedAt  time.Time
	cancelFlag int32 // atomic: 1 = cancelled
}

func (item *DownloadItem) markCancelled() { atomic.StoreInt32(&item.cancelFlag, 1) }
func (item *DownloadItem) isCancelled() bool {
	return atomic.LoadInt32(&item.cancelFlag) == 1
}

type PlaylistEntry struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	Title     string `json:"title"`
	Thumbnail string `json:"thumbnail"`
	Duration  string `json:"duration"`
}

var dlCounter int64

func NewApp() *App {
	return &App{
		schedCh:   make(chan struct{}, 1),
		maxActive: 1,
	}
}

// SetMaxConcurrent は自動補充で維持する実行中アイテム数の上限を設定する（1〜10 にクランプ）。
// 設定後に scheduler を起こし、引き上げ時は待ちキューから即座に補充させる。
func (a *App) SetMaxConcurrent(n int) {
	if n < 1 {
		n = 1
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
	truncateLog()
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
// docs/design.md「自動補充ルール（scheduler）」を参照。
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
	wailsruntime.EventsEmit(a.ctx, "download:update", *item)
}

func formatElapsed(d time.Duration) string {
	total := int(d.Seconds())
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

func formatDuration(secs int) string {
	if secs <= 0 {
		return ""
	}
	h := secs / 3600
	m := (secs % 3600) / 60
	s := secs % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
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

func ytDlpDir() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cfg, "moviedl")
	return dir, os.MkdirAll(dir, 0o755)
}

func (a *App) ytDlpPath() (string, error) {
	dir, err := ytDlpDir()
	if err != nil {
		return "", err
	}
	name := "yt-dlp"
	if goruntime.GOOS == "windows" {
		name = "yt-dlp.exe"
	}
	return filepath.Join(dir, name), nil
}

func ffmpegManagedPath() (string, error) {
	dir, err := ytDlpDir()
	if err != nil {
		return "", err
	}
	name := "ffmpeg"
	if goruntime.GOOS == "windows" {
		name = "ffmpeg.exe"
	}
	return filepath.Join(dir, name), nil
}

func ffmpegPath() string {
	if p, err := ffmpegManagedPath(); err == nil {
		if info, err := os.Stat(p); err == nil && info.Size() > 0 {
			return p
		}
	}
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		return p
	}
	// GUI apps on macOS don't inherit the shell PATH, so check Homebrew locations explicitly.
	for _, p := range []string{
		"/opt/homebrew/bin/ffmpeg", // Apple Silicon
		"/usr/local/bin/ffmpeg",    // Intel
	} {
		if info, err := os.Stat(p); err == nil && info.Size() > 0 {
			return p
		}
	}
	return ""
}

func (a *App) CheckFfmpeg() bool { return ffmpegPath() != "" }

// CanInstallFfmpeg はアプリ内 ffmpeg インストールに対応しているか返す（Windows のみ）。
func (a *App) CanInstallFfmpeg() bool { return goruntime.GOOS == "windows" }

func (a *App) FfmpegInstallHint() string {
	switch goruntime.GOOS {
	case "darwin":
		return "ffmpeg が見つかりません。高画質ダウンロードには brew install ffmpeg が必要です。"
	case "windows":
		return "ffmpeg が見つかりません。高画質ダウンロードにはインストールが必要です。"
	default:
		return "ffmpeg が見つかりません。パッケージマネージャーで ffmpeg をインストールしてください。"
	}
}

// InstallFfmpeg は Windows 向けに ffmpeg を取得して設定フォルダへ配置する。
// FFmpeg-Builds の win64-gpl zip を取得し、checksums.sha256 と照合してから
// zip 内の bin/ffmpeg.exe を原子的に配置する。詳細は docs/design.md「Windows ffmpeg の取得」参照。
func (a *App) InstallFfmpeg() error {
	if goruntime.GOOS != "windows" {
		return fmt.Errorf("このプラットフォームではアプリ内インストールに対応していません")
	}
	dest, err := ffmpegManagedPath()
	if err != nil {
		return err
	}
	const base = "https://github.com/yt-dlp/FFmpeg-Builds/releases/latest/download/"
	const asset = "ffmpeg-master-latest-win64-gpl.zip"
	client := &http.Client{Timeout: 15 * time.Minute}

	// 期待ダイジェストを先に取得（checksums.sha256 は <hex>␣␣<filename> 形式）。
	wantSum, err := fetchExpectedSum(client, base+"checksums.sha256", asset)
	if err != nil {
		return err
	}

	// zip を一時ファイルへストリーム保存しつつ SHA256 を計算する。
	resp, err := client.Get(base + asset)
	if err != nil {
		return fmt.Errorf("ダウンロード失敗: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ダウンロード失敗: HTTP %d", resp.StatusCode)
	}

	dir := filepath.Dir(dest)
	zipTmp, err := os.CreateTemp(dir, "ffmpeg-zip-*")
	if err != nil {
		return fmt.Errorf("一時ファイル作成失敗: %w", err)
	}
	zipName := zipTmp.Name()
	defer os.Remove(zipName) //nolint:errcheck

	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(zipTmp, h), resp.Body); err != nil {
		zipTmp.Close() //nolint:errcheck
		return fmt.Errorf("書き込み失敗: %w", err)
	}
	if err := zipTmp.Close(); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, wantSum) {
		return fmt.Errorf("チェックサム不一致（破損または改竄の可能性）: want=%s got=%s", wantSum, got)
	}

	// zip から bin/ffmpeg.exe を取り出して dest へ原子的に配置する。
	zr, err := zip.OpenReader(zipName)
	if err != nil {
		return fmt.Errorf("zip を開けません: %w", err)
	}
	defer zr.Close()
	names := make([]string, len(zr.File))
	for i, f := range zr.File {
		names[i] = f.Name
	}
	entry, err := ffmpegZipEntry(names)
	if err != nil {
		return err
	}
	var zf *zip.File
	for _, f := range zr.File {
		if f.Name == entry {
			zf = f
			break
		}
	}
	rc, err := zf.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	exeTmp, err := os.CreateTemp(dir, "ffmpeg-exe-*")
	if err != nil {
		return err
	}
	exeName := exeTmp.Name()
	defer os.Remove(exeName)                       //nolint:errcheck
	if _, err := io.Copy(exeTmp, rc); err != nil { //nolint:gosec // 取得元・SHA 照合済み
		exeTmp.Close() //nolint:errcheck
		return fmt.Errorf("展開失敗: %w", err)
	}
	if err := exeTmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(exeName, 0o755); err != nil {
		return err
	}
	if err := os.Rename(exeName, dest); err != nil {
		return fmt.Errorf("配置失敗: %w", err)
	}
	return nil
}

// ffmpegZipEntry は zip エントリ名一覧から basename が ffmpeg.exe のエントリを返す。
func ffmpegZipEntry(names []string) (string, error) {
	for _, n := range names {
		parts := strings.Split(n, "/")
		if parts[len(parts)-1] == "ffmpeg.exe" {
			return n, nil
		}
	}
	return "", fmt.Errorf("zip 内に ffmpeg.exe が見つかりません")
}

func (a *App) CheckYtDlp() bool {
	path, err := a.ytDlpPath()
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Size() > 0
}

// InstallYtDlp は yt-dlp を GitHub Releases から取得して配置する。
// SHA2-256SUMS の期待値とダウンロード実体の SHA256 を照合し、一致した場合のみ
// 最終パスへ原子的に配置する。詳細は docs/design.md「インストール時の完全性検証」を参照。
func (a *App) InstallYtDlp() error {
	path, err := a.ytDlpPath()
	if err != nil {
		return err
	}
	var assetName string
	switch goruntime.GOOS {
	case "darwin":
		assetName = "yt-dlp_macos"
	case "windows":
		assetName = "yt-dlp.exe"
	default:
		assetName = "yt-dlp"
	}
	const base = "https://github.com/yt-dlp/yt-dlp/releases/latest/download/"

	client := &http.Client{Timeout: 5 * time.Minute}

	// 期待ダイジェストを先に取得する
	wantSum, err := fetchExpectedSum(client, base+"SHA2-256SUMS", assetName)
	if err != nil {
		return err
	}

	// バイナリを一時ファイルへストリーム保存しつつ SHA256 を計算する
	resp, err := client.Get(base + assetName)
	if err != nil {
		return fmt.Errorf("ダウンロード失敗: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ダウンロード失敗: HTTP %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), "yt-dlp-dl-*")
	if err != nil {
		return fmt.Errorf("一時ファイル作成失敗: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) //nolint:errcheck // rename 成功後は no-op、失敗時は掃除

	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), resp.Body); err != nil {
		tmp.Close() //nolint:errcheck
		return fmt.Errorf("書き込み失敗: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	// 検証前の実体を最終パスに置かない: 一致を確認してから rename する
	gotSum := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(gotSum, wantSum) {
		return fmt.Errorf("チェックサム不一致（破損または改竄の可能性）: want=%s got=%s", wantSum, gotSum)
	}

	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("配置失敗: %w", err)
	}
	return nil
}

// fetchExpectedSum は SHA2-256SUMS を取得し、assetName 行の期待ダイジェストを返す。
func fetchExpectedSum(client *http.Client, url, assetName string) (string, error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("SHA2-256SUMS 取得失敗: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("SHA2-256SUMS 取得失敗: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	return parseSums(data, assetName)
}

// parseSums は SHA2-256SUMS の内容から assetName 行（"<hexdigest>  <filename>"）の
// 期待ダイジェストを返す。見つからなければエラー。
func parseSums(data []byte, assetName string) (string, error) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == assetName {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("SHA2-256SUMS に %s のエントリがありません", assetName)
}

// stripDedupSuffix は yt-dlp の内部 dedup アーティファクト（id が "-1" で終わり
// title が " (1)" で終わる）を検出し、付加された末尾の " (1)" を除去する。
// それ以外は title をそのまま返す。docs/design.md「(1) サフィックス問題」を参照。
func stripDedupSuffix(id, title string) string {
	if strings.HasSuffix(id, "-1") && strings.HasSuffix(title, " (1)") {
		return strings.TrimSuffix(title, " (1)")
	}
	return title
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

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("情報取得失敗: %w", err)
	}
	return parsePlaylistJSON(out)
}

// parsePlaylistJSON は yt-dlp --dump-json の出力（1 行 1 JSON）を解析する。
// webpage_url を優先し無ければ url を採用、URL 無し行とパース不能行はスキップする。
// 有効エントリが 0 件ならエラーを返す。docs/design.md「プレイリスト・ファイル選択」参照。
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

// isValidURL は yt-dlp に渡してよい URL かを判定する。
// http:// または https:// で始まることを要求し、引数インジェクション
// （URL が "-" 始まりで yt-dlp のオプションに化ける）を入口で防ぐ。
// 詳細は docs/design.md「引数インジェクション対策」を参照。
func isValidURL(raw string) bool {
	u, err := neturl.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// AddToQueue registers a URL as a queued item and notifies the scheduler.
// 不正な URL（http/https 以外）は空文字を返して登録を拒否する。
func (a *App) AddToQueue(url, outputDir string) string {
	if !isValidURL(url) {
		return ""
	}
	id := fmt.Sprintf("%d", atomic.AddInt64(&dlCounter, 1))
	item := &DownloadItem{
		ID:        id,
		URL:       url,
		outputDir: outputDir,
		Status:    "queued",
	}
	a.mu.Lock()
	a.items = append(a.items, item)
	a.mu.Unlock()

	a.emit(item)
	a.notify()
	return id
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
	if item != nil {
		item.Status = "paused"
	}
	a.mu.Unlock()
	if item == nil {
		return
	}
	if item.cmd != nil {
		suspendProcess(item.cmd) //nolint:errcheck
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
	if item != nil {
		item.Status = "downloading"
	}
	a.mu.Unlock()
	if item == nil {
		return
	}
	if item.cmd != nil {
		resumeProcess(item.cmd) //nolint:errcheck
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
	a.mu.Unlock()

	if item == nil {
		return
	}
	item.markCancelled()

	if item.cmd != nil && item.cmd.Process != nil {
		// Resume before killing so the process can receive SIGKILL on Unix.
		if item.Status == "paused" {
			resumeProcess(item.cmd) //nolint:errcheck
		}
		item.cmd.Process.Kill() //nolint:errcheck
	}

	if item.Status == "queued" || item.Status == "paused" || item.Status == "error" {
		item.Status = "cancelled"
		a.emit(item)
		a.removeItem(item.ID)
	}
}

func logPath() (string, error) {
	dir, err := ytDlpDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "moviedl.log"), nil
}

func truncateLog() {
	p, err := logPath()
	if err != nil {
		return
	}
	os.WriteFile(p, nil, 0o644) //nolint:errcheck
}

func openLogFile() *os.File {
	p, err := logPath()
	if err != nil {
		return nil
	}
	f, _ := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	return f
}

func logDirContents(lf *os.File, label, dir string) {
	if lf == nil {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		fmt.Fprintf(lf, "[%s] ReadDir %s: %v\n", time.Now().Format("15:04:05.000"), label, err)
		return
	}
	fmt.Fprintf(lf, "[%s] %s (%d entries):\n", time.Now().Format("15:04:05.000"), label, len(entries))
	for _, e := range entries {
		info, _ := e.Info()
		size := int64(-1)
		if info != nil {
			size = info.Size()
		}
		fmt.Fprintf(lf, "[%s]   %s (%d bytes)\n", time.Now().Format("15:04:05.000"), filepath.Join(dir, e.Name()), size)
	}
}

func (a *App) runDownload(item *DownloadItem) {
	defer a.notify()

	item.startedAt = time.Now()
	a.emit(item)

	lf := openLogFile()
	logf := func(format string, v ...any) {
		if lf != nil {
			fmt.Fprintf(lf, "[%s] "+format+"\n", append([]any{time.Now().Format("15:04:05.000")}, v...)...)
		}
	}
	if lf != nil {
		defer lf.Close()
	}

	// STEP 1: workDir を作る
	workDir, err := os.MkdirTemp(item.outputDir, ".moviedl-work-")
	if err != nil {
		item.Status = "error"
		item.Error = err.Error()
		a.emit(item)
		return
	}
	logf("[STEP1] workDir created")
	logf("[STEP1]   outputDir = %s", item.outputDir)
	logf("[STEP1]   workDir   = %s", workDir)
	logDirContents(lf, "[STEP1] workDir contents", workDir)
	registerWorkDir(workDir)
	defer func() {
		logf("[STEP6] cleanup: removing workDir")
		logDirContents(lf, "[STEP6] workDir contents at cleanup", workDir)
		os.RemoveAll(workDir)
		unregisterWorkDir(workDir)
	}()

	ytdlp, err := a.ytDlpPath()
	if err != nil {
		item.Status = "error"
		item.Error = err.Error()
		a.emit(item)
		return
	}

	// STEP 2: yt-dlp コマンドを組み立てる
	b := make([]byte, 8)
	rand.Read(b) //nolint:errcheck
	tmpBase := hex.EncodeToString(b)
	logf("[STEP2] tmpBase = %s", tmpBase)

	// STEP 2a: タイトルを事前取得
	ytdlpEnv := append(os.Environ(), "PYTHONUTF8=1", "PYTHONIOENCODING=utf-8")
	{
		var titleStderr strings.Builder
		titleCmd := exec.Command(ytdlp, "--skip-download", "--dump-json", "--no-playlist", "--", item.URL)
		titleCmd.Dir = workDir
		titleCmd.Env = ytdlpEnv
		titleCmd.Stderr = &titleStderr
		applyOSProcAttr(titleCmd)
		if out, err := titleCmd.Output(); err == nil {
			logf("[STEP2a] dump-json: %d bytes, stderr=%q", len(out), strings.TrimSpace(titleStderr.String()))
			firstLine := strings.TrimSpace(strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0])
			var dumpInfo struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			}
			if jerr := json.Unmarshal([]byte(firstLine), &dumpInfo); jerr == nil && dumpInfo.Title != "" {
				title := stripDedupSuffix(dumpInfo.ID, dumpInfo.Title)
				if title != dumpInfo.Title {
					logf("[STEP2a] dedup suffix stripped (raw id=%q)", dumpInfo.ID)
				}
				item.Title = title
				logf("[STEP2a] pre-fetched title: %q", title)
			} else {
				logf("[STEP2a] json parse error: %v", jerr)
			}
		} else {
			logf("[STEP2a] dump-json failed: %v, stderr=%q", err, strings.TrimSpace(titleStderr.String()))
		}
	}

	args := []string{
		"--newline", "--progress", "--no-mtime",
		"--encoding", "utf-8",
		"-o", tmpBase + ".%(ext)s",
		"-P", "home:" + workDir,
		"-P", "temp:" + workDir,
	}
	if ff := ffmpegPath(); ff != "" {
		args = append(args,
			"--ffmpeg-location", ff,
			"-f", "bestvideo+bestaudio/best",
			"--merge-output-format", "mp4",
		)
		logf("[STEP2] ffmpeg found: %s", ff)
	} else {
		args = append(args, "-f", "best[ext=mp4]/best")
		logf("[STEP2] ffmpeg not found, using single-format fallback")
	}
	args = append(args, "--", item.URL)
	logf("[STEP2] command: %s %s", ytdlp, strings.Join(args, " "))

	cmd := exec.Command(ytdlp, args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONIOENCODING=utf-8")
	applyOSProcAttr(cmd)

	a.mu.Lock()
	item.cmd = cmd
	a.mu.Unlock()

	pr, pw, err := os.Pipe()
	if err != nil {
		item.Status = "error"
		item.Error = err.Error()
		a.emit(item)
		return
	}
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		item.Status = "error"
		item.Error = err.Error()
		a.emit(item)
		return
	}
	pw.Close()

	// STEP 3: yt-dlp 実行中
	logf("[STEP3] yt-dlp started")
	scanner := bufio.NewScanner(pr)
	for scanner.Scan() {
		line := scanner.Text()
		logf("[STEP3] yt-dlp: %s", line)
		parseYtDlpLine(line, item)
		a.emit(item)
	}
	pr.Close()

	if err := cmd.Wait(); err != nil {
		if item.isCancelled() {
			item.Status = "cancelled"
		} else if item.Status != "finished" {
			item.Status = "error"
			item.Error = err.Error()
		}
		logf("[STEP3] wait error: %v (cancelled=%v)", err, item.isCancelled())
	} else {
		logf("[STEP3] yt-dlp finished")
		logDirContents(lf, "[STEP3] workDir contents after yt-dlp", workDir)

		// STEP 4: info.json があれば削除（タイトルは STEP2a で取得済み）
		finalTitle := item.Title
		if entries, _ := os.ReadDir(workDir); entries != nil {
			for _, e := range entries {
				if !strings.HasSuffix(e.Name(), ".info.json") {
					continue
				}
				jsonPath := filepath.Join(workDir, e.Name())
				logf("[STEP4] deleting info.json: %s", jsonPath)
				os.Remove(jsonPath) //nolint:errcheck
				logf("[STEP4] deleted info.json")
			}
		}
		if finalTitle == "" {
			logf("[STEP4] WARNING: no title found, will use tmpBase as filename")
		}
		logDirContents(lf, "[STEP4] workDir contents after info.json deleted", workDir)

		// STEP 5: 動画ファイルを outputDir へ移動する
		if entries, _ := os.ReadDir(workDir); entries != nil {
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				srcPath := filepath.Join(workDir, e.Name())
				ext := filepath.Ext(e.Name())
				var destName string
				if finalTitle != "" {
					destName = sanitizeFilename(finalTitle) + ext
				} else {
					destName = e.Name()
				}
				dst := uniqueDest(item.outputDir, destName)
				logf("[STEP5] src      = %s", srcPath)
				logf("[STEP5] destName = %s", destName)
				logf("[STEP5] dst      = %s", dst)
				if dst != filepath.Join(item.outputDir, destName) {
					logf("[STEP5] WARNING: uniqueDest added suffix (file already existed: %s)", filepath.Join(item.outputDir, destName))
				}
				if err := os.Rename(srcPath, dst); err != nil {
					logf("[STEP5] rename error: %v", err)
				} else {
					logf("[STEP5] rename OK")
				}
			}
		}
		logDirContents(lf, "[STEP5] outputDir contents after move", item.outputDir)

		item.Status = "finished"
		item.Percent = 100
		logDirContents(lf, "outputDir after download", item.outputDir)
	}
	a.emit(item)
	// エラー終了したアイテムはリストに残す（ユーザーがリトライまたは明示的に削除できるように）。
	// 詳細は docs/design.md「エラー終了したアイテムの扱い」を参照。
	if item.Status != "error" {
		a.removeItem(item.ID)
	}
}

// RetryDownload はエラー終了したアイテムを再キューする。
// docs/design.md「リトライ（RetryDownload）」を参照。
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
		item.Status = "queued"
		item.Error = ""
		item.Percent = 0
		item.Speed = ""
		item.ETA = ""
		item.Elapsed = ""
		item.TotalSize = ""
		atomic.StoreInt32(&item.cancelFlag, 0)
	}
	a.mu.Unlock()
	if item == nil {
		return
	}
	a.emit(item)
	a.notify()
}

func workDirRegistryPath() (string, error) {
	dir, err := ytDlpDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "workdirs.json"), nil
}

func readWorkDirRegistry() []string {
	p, err := workDirRegistryPath()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var dirs []string
	json.Unmarshal(data, &dirs) //nolint:errcheck
	return dirs
}

func writeWorkDirRegistry(dirs []string) {
	p, err := workDirRegistryPath()
	if err != nil {
		return
	}
	data, err := json.Marshal(dirs)
	if err != nil {
		return
	}
	os.WriteFile(p, data, 0o644) //nolint:errcheck
}

func registerWorkDir(path string) {
	dirs := readWorkDirRegistry()
	dirs = append(dirs, path)
	writeWorkDirRegistry(dirs)
}

func unregisterWorkDir(path string) {
	dirs := readWorkDirRegistry()
	filtered := dirs[:0]
	for _, d := range dirs {
		if d != path {
			filtered = append(filtered, d)
		}
	}
	writeWorkDirRegistry(filtered)
}

func cleanupLeftoverWorkDirs() {
	dirs := readWorkDirRegistry()
	for _, d := range dirs {
		os.RemoveAll(d) //nolint:errcheck
	}
	writeWorkDirRegistry(nil)
}

func sanitizeFilename(s string) string {
	r := strings.NewReplacer(`\`, "_", `/`, "_", `:`, "_", `*`, "_", `?`, "_", `"`, "_", `<`, "_", `>`, "_", `|`, "_")
	s = r.Replace(s)
	return strings.TrimRight(strings.TrimSpace(s), ". ")
}

func uniqueDest(dir, name string) string {
	dst := filepath.Join(dir, name)
	if _, err := os.Stat(dst); os.IsNotExist(err) {
		return dst
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", base, i, ext))
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
}

func parseYtDlpLine(line string, item *DownloadItem) {
	line = strings.TrimSpace(line)

	if strings.HasPrefix(line, "[download] Destination:") {
		return
	}
	if strings.Contains(line, "has already been downloaded") {
		item.Status = "finished"
		item.Percent = 100
		return
	}
	if !strings.HasPrefix(line, "[download]") {
		return
	}

	// [download]  45.3% of   10.00MiB at    1.50MiB/s ETA 00:03
	parts := strings.Fields(line)
	for i, p := range parts {
		if strings.HasSuffix(p, "%") {
			var pct float64
			fmt.Sscanf(strings.TrimSuffix(p, "%"), "%f", &pct)
			item.Percent = pct
			// 進捗 100% はダウンロード完了であって全体の成功ではない（ffmpeg 結合などの
			// 後処理が残る）。完了の確定は runDownload の成功分岐でのみ行う。
			// docs/design.md「finished は進捗 100% で決めてはならない」を参照。
		}
		if p == "of" && i+1 < len(parts) && parts[i+1] != "~" {
			item.TotalSize = parts[i+1]
		}
		if p == "at" && i+1 < len(parts) && parts[i+1] != "Unknown" {
			item.Speed = parts[i+1]
		}
		if p == "ETA" && i+1 < len(parts) && parts[i+1] != "Unknown" {
			item.ETA = parts[i+1]
		}
	}
}
