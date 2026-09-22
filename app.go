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
	"regexp"
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
		schedCh: make(chan struct{}, 1),
		// 既定は 0（登録のみモード）。起動直後に前回分が勝手に走り出さないようにする。
		// design.md「maxActive == 0（登録のみモード）」参照。1 に戻してはいけない。
		maxActive: 0,
		procs:     make(map[*exec.Cmd]struct{}),
	}
}

// registerProc は yt-dlp プロセスを登録簿に載せる。yt-dlp を起こす直前に呼ぶ。
// 更新中は false を返すので、呼び出し側は起動せずに中止しなければならない。
func (a *App) registerProc(cmd *exec.Cmd) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.updating {
		return false
	}
	a.procs[cmd] = struct{}{}
	return true
}

// unregisterProc はプロセス終了後に登録簿から外す。registerProc が true を
// 返した経路では必ず（defer で）呼ぶこと。
func (a *App) unregisterProc(cmd *exec.Cmd) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.procs, cmd)
}

// liveProcCount は生存している yt-dlp プロセス数を返す。
func (a *App) liveProcCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.procs)
}

// beginUpdate / endUpdate は yt-dlp 実行ファイルの置き換え区間を囲む。
// この間は registerProc が false を返し、新規の yt-dlp 起動が止まる。
func (a *App) beginUpdate() {
	a.mu.Lock()
	a.updating = true
	a.mu.Unlock()
}

func (a *App) endUpdate() {
	a.mu.Lock()
	a.updating = false
	a.mu.Unlock()
	a.notify()
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
// zip 内の bin/ffmpeg.exe を原子的に配置する。詳細は aidlc-docs/inception/application-design/design.md「Windows ffmpeg の取得」参照。
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
// 最終パスへ原子的に配置する。詳細は aidlc-docs/inception/application-design/design.md「インストール時の完全性検証」を参照。
func (a *App) InstallYtDlp() error {
	path, err := a.ytDlpPath()
	if err != nil {
		return err
	}
	tmpName, err := downloadYtDlpVerified(path)
	if err != nil {
		return err
	}
	defer os.Remove(tmpName) //nolint:errcheck // rename 成功後は no-op、失敗時は掃除
	return placeYtDlp(tmpName, path)
}

// downloadYtDlpVerified は最新の yt-dlp を一時ファイルへ取得し、SHA256 照合まで済ませて
// その一時ファイルのパスを返す。**最終パスには何も書かない**ため、この関数が失敗しても
// 既存の yt-dlp は無傷である。呼び出し側は成功時に placeYtDlp で配置し、
// いずれの場合も os.Remove で一時ファイルを掃除する責務を持つ。
// 一時ファイルは destPath と同じディレクトリに作る（同一 FS 内なので rename が原子的になる）。
func downloadYtDlpVerified(destPath string) (string, error) {
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
		return "", err
	}

	// バイナリを一時ファイルへストリーム保存しつつ SHA256 を計算する
	resp, err := client.Get(base + assetName)
	if err != nil {
		return "", fmt.Errorf("ダウンロード失敗: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ダウンロード失敗: HTTP %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp(filepath.Dir(destPath), "yt-dlp-dl-*")
	if err != nil {
		return "", fmt.Errorf("一時ファイル作成失敗: %w", err)
	}
	tmpName := tmp.Name()

	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), resp.Body); err != nil {
		tmp.Close()        //nolint:errcheck
		os.Remove(tmpName) //nolint:errcheck
		return "", fmt.Errorf("書き込み失敗: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName) //nolint:errcheck
		return "", err
	}

	// 検証前の実体を最終パスに置かない: 一致を確認してから配置する
	gotSum := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(gotSum, wantSum) {
		os.Remove(tmpName) //nolint:errcheck
		return "", fmt.Errorf("チェックサム不一致（破損または改竄の可能性）: want=%s got=%s", wantSum, gotSum)
	}
	return tmpName, nil
}

// placeYtDlp は検証済みの一時ファイルを最終パスへ原子的に配置する。
// Windows では実行中の .exe を上書きできないため、呼び出し側は事前にすべての
// yt-dlp プロセスを停止しておく責務を持つ（design.md「yt-dlp の更新（UpdateYtDlp）」）。
func placeYtDlp(tmpName, destPath string) error {
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmpName, destPath); err != nil {
		return fmt.Errorf("配置失敗: %w", err)
	}
	return nil
}

// stopTarget は更新のために停止する 1 アイテムを表す。
type stopTarget struct {
	item *DownloadItem
	// cmd はロック内で退避した item.cmd。STEP2a のタイトル取得中など、
	// アイテムに紐づくコマンドがまだ設定されていない場合は nil になる。
	cmd *exec.Cmd
	// needsResume はサスペンド中（一時停止）を表す。SIGSTOP 中のプロセスは
	// SIGKILL を受け取れないため、resume してから Kill する必要がある。
	needsResume bool
}

// planStop は更新のために停止すべきアイテムを、リストの順序を保って返す。
// 状態は変更しない（変更は呼び出し側の責務）。
// item.cmd を読むため **呼び出し側が a.mu を保持していること**。
// design.md「停止対象は『実行中』だけでは足りない」参照。
func planStop(items []*DownloadItem) []stopTarget {
	var out []stopTarget
	for _, it := range items {
		switch it.Status {
		case "downloading":
			out = append(out, stopTarget{item: it, cmd: it.cmd})
		case "paused":
			out = append(out, stopTarget{item: it, cmd: it.cmd, needsResume: true})
		}
	}
	return out
}

// UpdateImpact は更新時に停止される対象の内訳。確認ダイアログの文面に使う。
type UpdateImpact struct {
	// Items は停止して 0% からやり直しになるダウンロード数。
	Items int `json:"items"`
	// OtherProcs は情報取得中など、アイテムに紐づかない生存プロセス数。
	OtherProcs int `json:"otherProcs"`
}

// Affected は確認ダイアログを出すべきかを返す。
func (u UpdateImpact) Affected() bool { return u.Items > 0 || u.OtherProcs > 0 }

// computeUpdateImpact は影響の内訳を計算する。liveProcs は登録簿の生存プロセス数。
// アイテム 1 件が必ず 1 プロセスとは対応しない（STEP2a 中は item.cmd 未設定）ため、
// 差分は 0 で下限を切る。
func computeUpdateImpact(items []*DownloadItem, liveProcs int) UpdateImpact {
	affected := len(planStop(items))
	other := liveProcs - affected
	if other < 0 {
		other = 0
	}
	return UpdateImpact{Items: affected, OtherProcs: other}
}

// parseYtDlpVersion は `yt-dlp --version` の出力から version 文字列を取り出す。
func parseYtDlpVersion(out []byte) string {
	s := strings.TrimSpace(string(out))
	if s == "" {
		return ""
	}
	return strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
}

// GetYtDlpVersion はインストール済み yt-dlp のバージョンを返す（未インストール・
// 取得失敗時は空文字）。更新したかどうかをユーザーが確認できるようにするための表示用。
func (a *App) GetYtDlpVersion() string {
	ytdlp, err := a.ytDlpPath()
	if err != nil {
		return ""
	}
	if info, err := os.Stat(ytdlp); err != nil || info.Size() == 0 {
		return ""
	}
	cmd := exec.Command(ytdlp, "--version")
	applyOSProcAttr(cmd)
	if !a.registerProc(cmd) { // 更新中は起動しない
		return ""
	}
	defer a.unregisterProc(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return parseYtDlpVersion(out)
}

// YtDlpUpdateImpact は更新前に停止される対象の内訳を返す（確認ダイアログ用）。
func (a *App) YtDlpUpdateImpact() UpdateImpact {
	a.mu.Lock()
	impact := computeUpdateImpact(a.items, len(a.procs))
	a.mu.Unlock()
	return impact
}

// procDrainTimeout は停止指示後にプロセスの終了を待つ上限。
const procDrainTimeout = 10 * time.Second

// UpdateYtDlp は yt-dlp を最新版へ更新する。手動実行のみで、自動チェックは行わない。
//
// **手順の順序が要件である。** ダウンロードと SHA256 検証が成功してから停止・置き換えを行う。
// 先に停止するとネットワーク失敗時にユーザーの進捗だけが失われて何も得られない。
// 詳細は aidlc-docs/inception/application-design/design.md「yt-dlp の更新（UpdateYtDlp）」を参照。
func (a *App) UpdateYtDlp() error {
	path, err := a.ytDlpPath()
	if err != nil {
		return err
	}

	// STEP 1: 新版を取得して検証する。ここまでは既存環境への影響ゼロ。
	tmpName, err := downloadYtDlpVerified(path)
	if err != nil {
		return err
	}
	defer os.Remove(tmpName) //nolint:errcheck // 配置成功後は no-op、失敗時は掃除

	// STEP 2: 更新区間に入る。以降 registerProc が false を返し、新規 yt-dlp 起動が止まる。
	a.beginUpdate()
	defer a.endUpdate() // 解除時に notify するので、戻したアイテムはここで自動補充される

	// STEP 3: 生存している yt-dlp をすべて停止する。
	a.mu.Lock()
	targets := planStop(a.items)
	a.mu.Unlock()
	for _, t := range targets {
		t.item.markStoppedForUpdate() // Wait エラーを "error" ではなく再キュー扱いにする
	}
	stopTargets(targets)
	killed := a.killRegisteredProcs() // アイテムに紐づかない情報取得プロセスの取りこぼしを防ぐ

	// 停止を試みたツリーを集める（登録簿の排出だけでは孫プロセスを見られないため）。
	trees := make([]*exec.Cmd, 0, len(killed)+len(targets))
	trees = append(trees, killed...)
	for _, t := range targets {
		if t.cmd != nil {
			trees = append(trees, t.cmd)
		}
	}

	// STEP 4: プロセスが消えるのを待ってから配置する（Windows は実行中 .exe を上書き不可）。
	// 登録簿の排出（yt-dlp 本体）と、ツリーの消滅（ffmpeg 孫プロセス）の**両方**を待つ。
	// 孫を待たないと、実行ファイルを掴んだままの状態で置き換えて共有違反になる。
	if !a.waitProcsDrained(procDrainTimeout) || !waitTreesGone(trees, procDrainTimeout) {
		a.requeueStopped(targets)
		return fmt.Errorf("実行中の yt-dlp が終了しないため更新を中止しました。しばらく待って再度お試しください")
	}
	placeErr := placeYtDlp(tmpName, path)

	// STEP 5: 停止したアイテムを待ちキューへ戻す。**配置後に行うこと**。
	// 先に戻すと scheduler が置き換え前の古いバイナリで再開してしまう。
	a.requeueStopped(targets)
	return placeErr
}

// stopTargets は planStop が選んだアイテムのプロセスを停止する。
// サスペンド中は SIGKILL を受け取れないため resume してから Kill する
// （CancelDownload と同じ順序）。
//
// 停止はプロセス**ツリー**単位で行う。yt-dlp だけを止めると ffmpeg が孤児化して
// 実行ファイルを掴んだままになり、Windows では置き換えが共有違反で失敗する。
// design.md「プロセス管理（停止は孫プロセスまで及ばせる）」を参照。
func stopTargets(targets []stopTarget) {
	for _, t := range targets {
		if t.cmd == nil || t.cmd.Process == nil {
			continue
		}
		if t.needsResume {
			resumeProcessTree(t.cmd) //nolint:errcheck
		}
		killProcessTree(t.cmd) //nolint:errcheck
	}
}

// killRegisteredProcs は登録簿にあるすべての yt-dlp プロセスを Kill する。
// FetchPlaylist / STEP2a のタイトル取得はアイテム経由で停止できないため、
// これが取りこぼしを防ぐ backstop になる。
// 戻り値は停止を試みたコマンド。呼び出し側が waitTreesGone で
// 孫プロセスまで消えたことを確認できるようにするため返す。
func (a *App) killRegisteredProcs() []*exec.Cmd {
	a.mu.Lock()
	cmds := make([]*exec.Cmd, 0, len(a.procs))
	for c := range a.procs {
		cmds = append(cmds, c)
	}
	a.mu.Unlock()
	for _, c := range cmds {
		if c.Process != nil {
			// ツリー単位で止める。yt-dlp だけを Kill すると ffmpeg が孤児化し、
			// waitProcsDrained が「排出完了」と誤判定したまま実行ファイルを掴み続ける。
			killProcessTree(c) //nolint:errcheck
		}
	}
	return cmds
}

// waitTreesGone は停止したプロセスツリーが 1 つも残らなくなるまで待つ。
//
// **なぜ登録簿の排出だけでは足りないか:** 登録簿が数えるのは yt-dlp プロセスであって、
// yt-dlp が起動する ffmpeg 孫プロセスではない。yt-dlp が回収された後も ffmpeg が
// 実行ファイルを掴んだままなら、Windows では os.Rename での置き換えが共有違反で失敗する。
// design.md「プロセス管理（停止は孫プロセスまで及ばせる）」を参照。
func waitTreesGone(cmds []*exec.Cmd, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		remaining := false
		for _, c := range cmds {
			if !processTreeGone(c) {
				remaining = true
				break
			}
		}
		if !remaining {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitProcsDrained は登録簿が空になるまで待ち、空になれば true を返す。
func (a *App) waitProcsDrained(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if a.liveProcCount() == 0 {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// requeueStopped は停止したアイテムを待ちキューへ戻す。進捗は保持されないため
// 表示もリセットする（再開ではなく再実行になる。requirements.md に明記済み）。
func (a *App) requeueStopped(targets []stopTarget) {
	a.mu.Lock()
	for _, t := range targets {
		resetForRequeue(t.item)
	}
	a.mu.Unlock()
	for _, t := range targets {
		a.emit(t.item)
	}
}

// resetForRequeue は停止したアイテムを待ちキューへ戻す状態遷移。
// 進捗は引き継げないため表示もリセットする（workDir は実行ごとに破棄されるので
// 再開ではなく再実行になる。RetryDownload と同じ初期化を行う）。
// 呼び出し側が a.mu を保持していること。
func resetForRequeue(item *DownloadItem) {
	item.Status = "queued"
	item.Error = ""
	item.Percent = 0
	item.Speed = ""
	item.ETA = ""
	item.Elapsed = ""
	item.TotalSize = ""
	item.cmd = nil
	item.clearStoppedForUpdate()
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
// それ以外は title をそのまま返す。aidlc-docs/inception/application-design/design.md「(1) サフィックス問題」を参照。
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

	// 登録簿を通す（UpdateYtDlp がこのプロセスを停止できるようにするため）。
	if !a.registerProc(cmd) {
		return nil, fmt.Errorf("yt-dlp を更新中です。完了後に再度お試しください")
	}
	defer a.unregisterProc(cmd)

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("情報取得失敗: %w", err)
	}
	return parsePlaylistJSON(out)
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

// isValidURL は yt-dlp に渡してよい URL かを判定する。
// http:// または https:// で始まることを要求し、引数インジェクション
// （URL が "-" 始まりで yt-dlp のオプションに化ける）を入口で防ぐ。
// 詳細は aidlc-docs/inception/application-design/design.md「引数インジェクション対策」を参照。
func isValidURL(raw string) bool {
	u, err := neturl.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// isM3U8URL は URL の**パス**が .m3u8 で終わるかを判定する（大小無視）。
//
// m3u8 向けの分岐（Referer 付与・保存名の生成・登録時の警告）は**すべてこの述語に集約する**。
// 3 箇所が別々の判定を持つと、片方だけ直したときに挙動が食い違う。
//
// クエリ文字列は判定に含めない。m3u8 の URL は "...master.m3u8?token=x" のようにクエリ付きが
// 普通であり、生文字列の strings.HasSuffix では末尾が .m3u8 にならないため判定できない。
// 詳細は aidlc-docs/inception/application-design/design.md「判定は 1 つの述語に集約する」を参照。
func isM3U8URL(raw string) bool {
	if !isValidURL(raw) {
		return false
	}
	u, err := neturl.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return strings.HasSuffix(strings.ToLower(u.Path), ".m3u8")
}

// refererFor は m3u8 URL のオリジン（scheme://host/）を Referer として返す。
// m3u8 でない URL・不正な URL には "" を返し、呼び出し側は --referer を付けない。
//
// 付与を m3u8 に限定するのは**デグレ防止のため**。既存の 1000 以上の対応サイトは現在 Referer
// なしで動作しており、全 URL に送り始めるとそれらの経路の挙動を変えてしまう。
//
// 組み立てた値は isValidURL で再検証してから返す。yt-dlp のオプションに化ける値
// （"-" 始まり）を渡さないための多層防御で、design.md「引数インジェクション対策（-- 終端は必須）」
// と同じ思想。詳細は design.md「Referer は m3u8 URL に限って付与する」を参照。
func refererFor(raw string) string {
	if !isM3U8URL(raw) {
		return ""
	}
	u, err := neturl.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	// パス・クエリ・フラグメントは落とし、オリジンだけを渡す。
	origin := u.Scheme + "://" + u.Host + "/"
	if !isValidURL(origin) {
		return ""
	}
	return origin
}

// m3u8GenericPathSegments は保存名を組み立てるときに飛ばすパス要素。
// 配信構成上の定型語であって動画を識別しないため、これらを名前に入れても判別に役立たない。
var m3u8GenericPathSegments = map[string]bool{
	"hls": true, "stream": true, "streams": true, "media": true,
	"video": true, "videos": true, "playlist": true, "manifest": true,
	"out": true, "vod": true,
}

// m3u8FileName は m3u8 URL から「ホスト名 + 意味のあるパス要素 + 日時」の拡張子なし base 名を返す。
// m3u8 でない URL には "" を返す。
//
// なぜ必要か: HLS の m3u8 にはタイトルのメタデータがないため、yt-dlp が返す title は
// m3u8 のファイル名そのもの（master / index / playlist）になる。そのままでは保存名が
// master.mp4 に集中し、2 本目以降が uniqueDest によって "master (1).mp4" になって判別できない。
//
// クエリ文字列は含めない。トークンで読めない名前になるうえ、ログのマスク方針と矛盾する。
// 詳細は aidlc-docs/inception/application-design/design.md「保存名は URL だけから組み立てる」を参照。
func m3u8FileName(raw string, now time.Time) string {
	if !isM3U8URL(raw) {
		return ""
	}
	u, err := neturl.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	// Host ではなく Hostname を使う。ポートを含めるとファイル名に ':' が入ってしまう。
	parts := []string{u.Hostname()}
	if seg := meaningfulPathSegment(u.Path); seg != "" {
		parts = append(parts, seg)
	}
	parts = append(parts, now.Format("20060102-1504"))
	// 呼び出し側（STEP5）も sanitizeFilename を通すが、ここで通しておくことで
	// 「m3u8FileName の戻り値はそのままファイル名として使える」を関数の契約にできる。
	// sanitizeFilename は冪等なので二重適用は無害。
	return sanitizeFilename(strings.Join(parts, "_"))
}

// meaningfulPathSegment は .m3u8 のファイル名を除いたパス要素を末尾から走査し、
// 汎用語でない最初の要素を返す。見つからなければ "" を返す。
func meaningfulPathSegment(path string) string {
	segs := strings.Split(path, "/")
	if len(segs) > 0 {
		segs = segs[:len(segs)-1] // 末尾の要素は .m3u8 のファイル名なので捨てる
	}
	for i := len(segs) - 1; i >= 0; i-- {
		s := strings.TrimSpace(segs[i])
		if s == "" || s == "." || s == ".." {
			continue
		}
		if m3u8GenericPathSegments[strings.ToLower(s)] {
			continue
		}
		return s
	}
	return ""
}

// resolveTitle は保存名の元になるタイトルを決める。
// m3u8 URL なら STEP2a で取得したタイトルを捨てて m3u8FileName の生成名を使い、
// それ以外は取得したタイトルをそのまま使う（既存サイトの挙動を変えない）。
//
// なぜ「取得タイトルが汎用語のときだけ上書き」にしないか: パスが .m3u8 で終わる URL は
// generic エクストラクターが処理し、title は必ず m3u8 のファイル名になる（実測）。
// 条件を足すとどちらの名前が使われるかが URL によって変わり、純粋関数で決まらなくなる。
func resolveTitle(url, fetchedTitle string, now time.Time) string {
	if name := m3u8FileName(url, now); name != "" {
		return name
	}
	return fetchedTitle
}

// urlInLogPattern はログ行から http(s) URL を拾う。空白と引用符で止める。
var urlInLogPattern = regexp.MustCompile(`https?://[^\s"']+`)

// redactedQuery はマスク後のクエリ部分を表す。再適用しても同じ形になるため redactLine は冪等。
const redactedQuery = "?<redacted>"

// redactLine は文字列中の http(s) URL のクエリ部分を "?<redacted>" に置き換える。
//
// なぜ必要か: moviedl.log（os.UserConfigDir()/moviedl/ 内・0644・平文）には実行コマンド全体と
// yt-dlp の出力行が追記される。m3u8 の URL は**有効期限トークンが実質的な認可情報**であり、
// クエリ文字列ごと平文で残るのは機微情報の出力にあたる（SECURITY-03）。
//
// パスは残す（どのファイルで失敗したかの調査に必要）。落とすのはクエリだけ。
// neturl.Parse に依存せず文字列操作だけで実装するのは、パース不能な行でも確実にマスクするため。
// 詳細は aidlc-docs/inception/application-design/design.md「ログにトークン付き URL を残さない」を参照。
func redactLine(s string) string {
	return urlInLogPattern.ReplaceAllStringFunc(s, func(u string) string {
		i := strings.IndexByte(u, '?')
		if i < 0 {
			return u
		}
		return u[:i] + redactedQuery
	})
}

// logLine は runDownload のログ 1 行を組み立てる。
//
// **組み立ての最後に redactLine を通すのが要点。** 各呼び出し側で redactLine を呼ぶ設計にすると
// 「1 箇所でも漏れたらその経路だけ穴が空く」（applyOSProcAttr や -- 終端と同じ性質の）ルールが
// また 1 つ増える。ここで一度だけ通せば、logf の呼び出し側は URL を含む値をそのまま渡してよく、
// マスク漏れが構造的に起きない。
func logLine(now time.Time, format string, v ...any) string {
	return redactLine(fmt.Sprintf("[%s] "+format, append([]any{now.Format("15:04:05.000")}, v...)...)) + "\n"
}

// isKillablePID は停止対象として正当な pid かを返す。1 以下を拒否する。
//
// **なぜ独立した述語にするか:** Unix の kill(2) は第 1 引数の符号と値で意味が変わる。
//
//	pid > 0        → そのプロセスのみ
//	-pgid (pgid>1) → そのプロセスグループの全員 ← これが狙い
//	-1             → 呼び出し元が権限を持つ全プロセス ← アプリごと巻き込んで殺す
//	0              → 呼び出し元自身のプロセスグループ ← アプリ自身を殺す
//
// cmd.Process が異常な pid を持つ経路で符号を反転させると kill(0, ...) や kill(-1, ...) が
// 成立しうる。requirements.md のセキュリティ要件「停止対象をそのダウンロードのために
// 起動したプロセスに限定する」に直結するため、反転の前に必ずここを通す。
// Windows 側でもジョブから列挙した pid の妥当性確認に使う。
// 詳細は aidlc-docs/inception/application-design/design.md「Kill(-pid) の符号は致命的に危険」を参照。
func isKillablePID(pid int) bool { return pid > 1 }

// isYtDlpErrorLine は yt-dlp の出力行がエラー行かを返す。
// 進捗行と混ざった stdout/stderr から、ユーザーに見せるべき行だけを拾うために使う。
func isYtDlpErrorLine(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "ERROR:")
}

// explainDownloadError はダウンロード失敗の理由をユーザー向けの文言にする。
//
// 既存の実装は cmd.Wait() の err（"exit status 1"）をそのまま Error に入れていたため、
// なぜ失敗したのかがユーザーに伝わらなかった。yt-dlp が出したエラー行を使って説明する。
//
// m3u8 の 403 は**有効期限切れ**が典型的な原因で、しかも技術的な対策が存在しない
// （ユーザーが元ページから URL を取り直すしかない）。そのことを明示的に案内する。
// requirements.md「期限切れ URL」を参照。
//
// 戻り値は必ず redactLine を通す。この文言は画面に出るうえ URL コピーで持ち出されるため、
// トークンを載せてはいけない（SECURITY-03）。
func explainDownloadError(lastErrLine, url, waitErr string) string {
	line := strings.TrimSpace(lastErrLine)
	if line == "" {
		return waitErr
	}
	if strings.Contains(line, "403") {
		if isM3U8URL(url) {
			return redactLine("アクセスが拒否されました（403）。m3u8 の URL が失効した可能性があります。" +
				"元のページから URL を取り直してください。 / " + line)
		}
		return redactLine("アクセスが拒否されました（403）。 / " + line)
	}
	return redactLine(line)
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

// AddToQueue registers a URL as a queued item and notifies the scheduler.
// 不正な URL（http/https 以外）、および既存アイテムと同一 URL（重複）は
// 空文字を返して登録を拒否する。重複チェックと append は同一ロック区間で行う
// （Wails の各 IPC は別 goroutine のため、同一 URL の同時登録を防ぐ）。
func (a *App) AddToQueue(url, outputDir string) string {
	if !isValidURL(url) {
		return ""
	}
	a.mu.Lock()
	if containsURL(a.items, url) {
		a.mu.Unlock()
		return ""
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
			// 組み立ては logLine に一任する（redactLine を通してトークンを残さない）。
			fmt.Fprint(lf, logLine(time.Now(), format, v...))
		}
	}
	if lf != nil {
		defer lf.Close()
	}

	// STEP 1: workDir を作る
	workDir, err := os.MkdirTemp(item.outputDir, workDirPrefix)
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
		// 登録簿を通してから起動する。更新中は起動せず事前取得を諦める
		// （タイトルは STEP4 の info.json で補える）。
		if !a.registerProc(titleCmd) {
			logf("[STEP2a] skipped: yt-dlp 更新中のため起動しない")
		} else {
			out, err := titleCmd.Output()
			a.unregisterProc(titleCmd) // 短命なので defer せず即座に外す（生存数を正確に保つため）
			if err == nil {
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
	}

	// STEP 2b: m3u8 は取得タイトルが m3u8 のファイル名（master / index など）になるため、
	// URL から判別可能な保存名を組み立てて上書きする。
	// design.md「保存名は URL だけから組み立てる（m3u8FileName）」を参照。
	if resolved := resolveTitle(item.URL, item.Title, time.Now()); resolved != item.Title {
		logf("[STEP2b] m3u8 のため保存名を URL から生成: %q （取得タイトル %q を使わない）", resolved, item.Title)
		item.Title = resolved
	}

	ff := ffmpegPath()
	if ff != "" {
		logf("[STEP2] ffmpeg found: %s", ff)
	} else {
		logf("[STEP2] ffmpeg not found, using single-format fallback")
	}
	args := buildYtDlpArgs(tmpBase, workDir, ff, item.URL)
	logf("[STEP2] command: %s %s", ytdlp, strings.Join(args, " "))

	cmd := exec.Command(ytdlp, args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONIOENCODING=utf-8")
	applyOSProcAttr(cmd)

	// 登録簿を通してから起動する。更新中は起動せず待ちキューへ戻し、
	// 更新完了時の notify で再開させる（エラーにはしない）。
	if !a.registerProc(cmd) {
		logf("[STEP2] yt-dlp 更新中のため待ちキューへ戻す")
		a.mu.Lock()
		item.Status = "queued"
		a.mu.Unlock()
		a.emit(item)
		return
	}
	defer a.unregisterProc(cmd)

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

	// Start 直後にプロセスツリーの追跡を開始する（Windows は Job Object へ割り当て）。
	// 非 Windows は applyOSProcAttr の Setpgid で完結しているため no-op。
	// design.md「プロセス管理（停止は孫プロセスまで及ばせる）」を参照。
	trackProcessTree(cmd)
	defer releaseProcessTree(cmd)

	// STEP 3: yt-dlp 実行中
	logf("[STEP3] yt-dlp started")
	// 最後に見たエラー行を覚えておく。cmd.Wait() の err は "exit status 1" でしかなく、
	// なぜ失敗したのかがユーザーに伝わらないため（403 の案内に使う）。
	var lastErrLine string
	scanner := bufio.NewScanner(pr)
	for scanner.Scan() {
		line := scanner.Text()
		logf("[STEP3] yt-dlp: %s", line)
		if isYtDlpErrorLine(line) {
			lastErrLine = line
		}
		parseYtDlpLine(line, item)
		a.emit(item)
	}
	pr.Close()

	if err := cmd.Wait(); err != nil {
		if item.isCancelled() {
			item.Status = "cancelled"
		} else if item.isStoppedForUpdate() {
			// yt-dlp 更新のために Kill された。エラーではなく待ちキューへ戻す扱いにする
			// （UpdateYtDlp が配置完了後に requeueStopped で状態を確定させる）。
			item.Status = "queued"
			item.Error = ""
			item.clearStoppedForUpdate()
			logf("[STEP3] stopped for yt-dlp update -> requeued")
		} else if item.Status != "finished" {
			item.Status = "error"
			// "exit status 1" ではなく yt-dlp のエラー行に基づく説明を出す。
			// m3u8 の 403 は URL の失効が典型なので取り直しを案内する（requirements.md「期限切れ URL」）。
			item.Error = explainDownloadError(lastErrLine, item.URL, err.Error())
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
					// uniqueDest が予約した 0 バイトプレースホルダーが残らないよう後始末する。
					os.Remove(dst) //nolint:errcheck
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
	if shouldRemoveWhenDone(item.Status) {
		a.removeItem(item.ID)
	}
}

// shouldRemoveWhenDone は runDownload 終了時にアイテムをリストから取り除くべきかを返す。
//
// 残す状態:
//   - "error": ユーザーがリトライ・URL コピー・明示的な削除をできるように残す
//     （design.md「エラー終了したアイテムの扱い」）
//   - "queued": yt-dlp 更新のために停止して待ちキューへ戻したアイテムがここを通る。
//     取り除くと再開できるはずのアイテムが消える
//     （design.md「yt-dlp の更新（UpdateYtDlp）」）
func shouldRemoveWhenDone(status string) bool {
	return status != "error" && status != "queued"
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

// workDirPrefix は runDownload が outputDir 内に作る一時作業ディレクトリ名の
// プレフィックス（os.MkdirTemp のパターン）。cleanupLeftoverWorkDirs の削除ガードにも使う。
const workDirPrefix = ".moviedl-work-"

// isManagedWorkDir は path がこのアプリの作業ディレクトリ（basename が
// workDirPrefix 始まり）かを判定する。cleanupLeftoverWorkDirs はこのガードを
// 通過したパスだけを os.RemoveAll するため、workdirs.json が改竄・破損して
// 不正なパスが混入しても任意ディレクトリを削除しない。
// aidlc-docs/inception/application-design/design.md「workDir 削除はプレフィックス検証必須」参照。
func isManagedWorkDir(path string) bool {
	return strings.HasPrefix(filepath.Base(path), workDirPrefix)
}

func cleanupLeftoverWorkDirs() {
	dirs := readWorkDirRegistry()
	for _, d := range dirs {
		if isManagedWorkDir(d) {
			os.RemoveAll(d) //nolint:errcheck
		}
	}
	writeWorkDirRegistry(nil)
}

func sanitizeFilename(s string) string {
	// リモートタイトル由来の制御文字（C0 制御 + DEL）を除去する。改行などが
	// ファイル名に混入すると保存失敗やログ汚染の原因になる。
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	r := strings.NewReplacer(`\`, "_", `/`, "_", `:`, "_", `*`, "_", `?`, "_", `"`, "_", `<`, "_", `>`, "_", `|`, "_")
	s = r.Replace(s)
	s = strings.TrimRight(strings.TrimSpace(s), ". ")
	// Windows 予約デバイス名（CON, NUL, COM1〜9 等）はそのままだとファイル作成に
	// 失敗するため、先頭に _ を付けて回避する。
	if isWindowsReservedName(s) {
		s = "_" + s
	}
	return s
}

// isWindowsReservedName は name（拡張子は無視）が Windows の予約デバイス名かを判定する。
// 大文字小文字は区別しない。CON / PRN / AUX / NUL と COM1〜9 / LPT1〜9 が対象。
func isWindowsReservedName(name string) bool {
	base := strings.ToUpper(strings.TrimSuffix(name, filepath.Ext(name)))
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(base) == 4 && base[3] >= '1' && base[3] <= '9' {
		switch base[:3] {
		case "COM", "LPT":
			return true
		}
	}
	return false
}

// uniqueDest は dir 配下に name の衝突しない保存先パスを返す。既存ファイルは
// 決して上書きせず、衝突時は " (1)" " (2)"… の連番を付ける。
//
// 単に Stat で空きを確認して返すのではなく、O_CREATE|O_EXCL で 0 バイトの
// プレースホルダーを作って名前をアトミックに予約する。これにより「空きを
// 確認してから rename するまで」の TOCTOU 窓（並行ダウンロードで同じタイトルを
// 同時取得した際に同名を選んでしまう競合や、外部プロセスが割り込む競合）を閉じる。
// 呼び出し側は返ったパスへ実体を os.Rename する（os.Rename は Unix/Windows とも
// このプレースホルダーを置換する）。rename に失敗した場合は os.Remove で後始末する。
// aidlc-docs/inception/application-design/design.md「uniqueDest について」参照。
func uniqueDest(dir, name string) string {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 0; ; i++ {
		candidate := filepath.Join(dir, name)
		if i > 0 {
			candidate = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", base, i, ext))
		}
		f, err := os.OpenFile(candidate, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			f.Close() //nolint:errcheck
			return candidate
		}
		if !os.IsExist(err) {
			// 予約できない予期せぬエラー（権限など）。従来挙動に倣い候補をそのまま返し、
			// rename 側でエラーをハンドリングさせる。
			return candidate
		}
	}
}

// buildYtDlpArgs は yt-dlp に渡す引数列を組み立てる。ffmpegLoc が空文字なら
// ffmpeg なし（単一フォーマット）のフォールバックになる。
//
// 堅牢化: yt-dlp の既定は取得失敗した断片を黙ってスキップして続行するため、
// 一時的なネットワーク不調で映像が途中で止まる・無音になったまま「成功」扱いに
// なりうる。--abort-on-unavailable-fragment で握りつぶさず中断（エラー）させ、
// --retries / --fragment-retries / --socket-timeout で一時障害を吸収する。
// aidlc-docs/inception/application-design/design.md「ダウンロードの堅牢化」を参照。
func buildYtDlpArgs(tmpBase, workDir, ffmpegLoc, url string) []string {
	args := []string{
		"--newline", "--progress", "--no-mtime",
		"--encoding", "utf-8",
		"--retries", "10",
		"--fragment-retries", "10",
		"--abort-on-unavailable-fragment",
		"--socket-timeout", "30",
		"-o", tmpBase + ".%(ext)s",
		"-P", "home:" + workDir,
		"-P", "temp:" + workDir,
	}
	if ffmpegLoc != "" {
		args = append(args,
			"--ffmpeg-location", ffmpegLoc,
			"-f", "bestvideo+bestaudio/best",
			"--merge-output-format", "mp4",
		)
	} else {
		args = append(args, "-f", "best[ext=mp4]/best")
	}
	// m3u8 URL のときだけ Referer を渡す（m3u8 を配信するサーバは Referer を要求することがあり、
	// 素の URL では 403 になる）。m3u8 以外に付けると既存の対応サイトの挙動を変えるため付けない。
	// design.md「Referer は m3u8 URL に限って付与する」を参照。
	if ref := refererFor(url); ref != "" {
		args = append(args, "--referer", ref)
	}
	args = append(args, "--", url)
	return args
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
			// aidlc-docs/inception/application-design/design.md「finished は進捗 100% で決めてはならない」を参照。
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
