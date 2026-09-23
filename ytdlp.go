// ytdlp.go: yt-dlp の配置・インストール・更新（UpdateYtDlp）。

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"
)

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
