package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	ctx    context.Context
	mu     sync.Mutex
	items  []*DownloadItem
	workCh chan *DownloadItem
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
	Status    string  `json:"status"` // "queued"|"downloading"|"finished"|"error"|"cancelled"
	Error     string  `json:"error,omitempty"`

	outputDir   string
	cmd         *exec.Cmd
	startedAt   time.Time
	cancelFlag  int32 // atomic: 1 = cancelled
}

func (item *DownloadItem) markCancelled() { atomic.StoreInt32(&item.cancelFlag, 1) }
func (item *DownloadItem) isCancelled() bool { return atomic.LoadInt32(&item.cancelFlag) == 1 }

var dlCounter int64

func NewApp() *App {
	return &App{
		workCh: make(chan *DownloadItem, 100),
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.extractEmbeddedFfmpeg() //nolint:errcheck  // must finish before frontend calls CheckFfmpeg
	truncateLog()
	cleanupLeftoverWorkDirs()
	go a.worker()
}

func (a *App) worker() {
	for item := range a.workCh {
		if item.isCancelled() {
			continue
		}
		a.runDownload(item)
	}
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

func (a *App) extractEmbeddedFfmpeg() error {
	embeddedName := "embedded/ffmpeg"
	if goruntime.GOOS == "windows" {
		embeddedName = "embedded/ffmpeg.exe"
	}
	data, err := embeddedFS.ReadFile(embeddedName)
	if err != nil {
		return nil
	}
	dest, err := ffmpegManagedPath()
	if err != nil {
		return err
	}
	if info, err := os.Stat(dest); err == nil && info.Size() == int64(len(data)) {
		return nil
	}
	return os.WriteFile(dest, data, 0o755)
}

func (a *App) CheckFfmpeg() bool { return ffmpegPath() != "" }

func (a *App) FfmpegInstallHint() string {
	switch goruntime.GOOS {
	case "darwin":
		return "ffmpeg が見つかりません。高画質ダウンロードには brew install ffmpeg が必要です。"
	case "windows":
		return "ffmpeg が見つかりません。リリース版バイナリには ffmpeg が同梱されています。"
	default:
		return "ffmpeg が見つかりません。パッケージマネージャーで ffmpeg をインストールしてください。"
	}
}

func (a *App) CheckYtDlp() bool {
	path, err := a.ytDlpPath()
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Size() > 0
}

func (a *App) InstallYtDlp() error {
	path, err := a.ytDlpPath()
	if err != nil {
		return err
	}
	var dlURL string
	switch goruntime.GOOS {
	case "darwin":
		dlURL = "https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp_macos"
	case "windows":
		dlURL = "https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp.exe"
	default:
		dlURL = "https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp"
	}
	resp, err := http.Get(dlURL) //nolint:noctx
	if err != nil {
		return fmt.Errorf("ダウンロード失敗: %w", err)
	}
	defer resp.Body.Close()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("ファイル作成失敗: %w", err)
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

func (a *App) AddToQueue(url, outputDir string) string {
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
	a.workCh <- item
	return id
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

	// Kill running process if any.
	if item.cmd != nil && item.cmd.Process != nil {
		item.cmd.Process.Kill() //nolint:errcheck
	}

	// If still queued (worker hasn't picked it up yet), update status immediately.
	if item.Status == "queued" {
		item.Status = "cancelled"
		a.emit(item)
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
	item.Status = "downloading"
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
	// --print "%(title)s" は出力テンプレート評価を経るため、yt-dlp が重複 ID を検出すると " (1)" を付加する。
	// --dump-json は生の info dict を返す。id が "-1" で終わり title が " (1)" で終わる場合は
	// yt-dlp の内部 dedup アーティファクトなので除去する。
	ytdlpEnv := append(os.Environ(), "PYTHONUTF8=1", "PYTHONIOENCODING=utf-8")
	{
		var titleStderr strings.Builder
		titleCmd := exec.Command(ytdlp, "--skip-download", "--dump-json", "--no-playlist", item.URL)
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
				title := dumpInfo.Title
				// id が "-1" で終わり title が " (1)" で終わる場合は yt-dlp dedup アーティファクト。
				// 両方マッチした場合のみ除去する（実タイトルに " (1)" が含まれる場合は id 末尾が "-1" にならないため保護される）。
				if strings.HasSuffix(dumpInfo.ID, "-1") && strings.HasSuffix(title, " (1)") {
					title = strings.TrimSuffix(title, " (1)")
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
	args = append(args, item.URL)
	logf("[STEP2] command: %s %s", ytdlp, strings.Join(args, " "))

	cmd := exec.Command(ytdlp, args...)
	cmd.Dir = workDir // CWD を workDir にすることで yt-dlp のタイトルベース競合チェックが Downloads を見ない
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

// cleanupLeftoverWorkDirs removes any work directories left over from a previous crash.
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

// moveToOutputDir moves completed files from workDir to outputDir.
// Subdirectories (e.g. the temp/ download dir) are skipped.
// If a same-named file already exists in outputDir, a numeric suffix is added.
func moveToOutputDir(workDir, outputDir string) error {
	entries, err := os.ReadDir(workDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		dst := uniqueDest(outputDir, e.Name())
		if err := os.Rename(filepath.Join(workDir, e.Name()), dst); err != nil {
			return err
		}
	}
	return nil
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
			if pct >= 100 {
				item.Status = "finished"
			}
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
