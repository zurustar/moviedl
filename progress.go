// progress.go: yt-dlp の進捗行の解析と、経過時間・長さの表示整形。

package main

import (
	"fmt"
	"strings"
	"time"
)

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
