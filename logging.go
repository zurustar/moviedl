// logging.go: moviedl.log への記録（セッション管理・トークンのマスク）。

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

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

// maxLogBytes はログを 1 世代退避する閾値。
const maxLogBytes = 5 << 20 // 5 MiB

// logDirOverride はログ出力先の差し替え口。通常は空で、テストだけが設定する。
// テストが実ログへ書くと、調査したい本物の記録をノイズで汚してしまうため。
var logDirOverride string

func logPath() (string, error) {
	if logDirOverride != "" {
		return filepath.Join(logDirOverride, "moviedl.log"), nil
	}
	dir, err := ytDlpDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "moviedl.log"), nil
}

// shouldRotateLog はログを退避すべきサイズかを返す。
func shouldRotateLog(size int64) bool { return size >= maxLogBytes }

// startLogSession は起動時に呼ぶ。**前回のログを消してはいけない。**
//
// 以前は truncateLog でログを空にしていたが、問題を再現した後にアプリを再起動すると
// 証拠が失われる。実際に「登録した URL が消えた」事象の調査時、ログは 0 バイトだった。
// 「再現してから再起動しないでください」と要求するのは調査手順として現実的でない。
// design.md「ログのセッション管理」を参照。
func startLogSession() {
	p, err := logPath()
	if err != nil {
		return
	}
	// 際限なく膨らませないため、上限を超えたときだけ 1 世代退避する。
	// ログを消す経路はここだけに限る。
	if fi, err := os.Stat(p); err == nil && shouldRotateLog(fi.Size()) {
		os.Rename(p, p+".1") //nolint:errcheck // 前回世代は上書きされる
	}
	appendLog("=== session start === version=%s", formatVersion(version, buildDate))
}

// appendLog は低頻度のイベント（登録・拒否・情報取得）を 1 行追記する。
//
// runDownload の高頻度な進捗行は開いたままのハンドル（logf）を使う。こちらは
// 呼び出しごとに開閉するが、頻度が低いので問題にならない。
// logLine 経由なのでトークンのマスクを必ず通る（SECURITY-03）。
func appendLog(format string, v ...any) {
	lf := openLogFile()
	if lf == nil {
		return
	}
	defer lf.Close()
	fmt.Fprint(lf, logLine(time.Now(), format, v...))
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
