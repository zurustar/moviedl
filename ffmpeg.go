// ffmpeg.go: ffmpeg の探索と、Windows 向けのアプリ内インストール。

package main

import (
	"archive/zip"
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
