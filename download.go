// download.go: 1 件のダウンロードの実行（runDownload）と yt-dlp 引数の組み立て。

package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

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
	args := buildYtDlpArgs(tmpBase, workDir, ff, item.URL, item.Referer)
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

// buildYtDlpArgs は yt-dlp に渡す引数列を組み立てる。ffmpegLoc が空文字なら
// ffmpeg なし（単一フォーマット）のフォールバックになる。
//
// 堅牢化: yt-dlp の既定は取得失敗した断片を黙ってスキップして続行するため、
// 一時的なネットワーク不調で映像が途中で止まる・無音になったまま「成功」扱いに
// なりうる。--abort-on-unavailable-fragment で握りつぶさず中断（エラー）させ、
// --retries / --fragment-retries / --socket-timeout で一時障害を吸収する。
// aidlc-docs/inception/application-design/design.md「ダウンロードの堅牢化」を参照。
// refererOverride にはユーザーが指定した元ページ URL を渡す（未指定なら空文字）。
// 未指定・不正なら m3u8 の自動導出に落ちる（effectiveReferer を参照）。
func buildYtDlpArgs(tmpBase, workDir, ffmpegLoc, url, refererOverride string) []string {
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
	// Referer を渡す（m3u8 を配信するサーバは Referer を要求することがあり、素の URL では
	// 403 になる）。ユーザー指定の元ページ URL があればそれを優先し、なければ m3u8 の
	// オリジンを自動導出する。自動導出を m3u8 に限定しているのは、既存の対応サイトの
	// 挙動を変えないため（デグレ防止）。
	// design.md「Referer は m3u8 URL に限って付与する」「Referer のユーザー指定」を参照。
	if ref := effectiveReferer(url, refererOverride); ref != "" {
		args = append(args, "--referer", ref)
	}
	args = append(args, "--", url)
	return args
}
