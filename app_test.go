package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIsValidURL(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		// 正常系
		{"https://example.com/watch?v=abc", true},
		{"http://example.com/v/1", true},
		{"  https://example.com/x  ", true}, // 前後空白はトリムされる

		// 引数インジェクション対策で弾くべき入力
		{"--exec=touch /tmp/pwned", false},
		{"-J", false},
		{"--config-location=/etc/evil.conf", false},

		// スキーム不正・不足
		{"ftp://example.com/x", false},
		{"file:///etc/passwd", false},
		{"example.com", false}, // スキームなし
		{"https://", false},    // ホストなし
		{"", false},
	}
	for _, c := range cases {
		if got := isValidURL(c.in); got != c.want {
			t.Errorf("isValidURL(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// 仕様: docs/design.md「sanitizeFilename について」
// 禁止文字（\ / : * ? " < > |）を _ に置換し、前後の空白と末尾のドットを除去する。
func TestSanitizeFilename(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Never Gonna Give You Up", "Never Gonna Give You Up"},
		{`a/b\c:d*e?f"g<h>i|j`, "a_b_c_d_e_f_g_h_i_j"},
		{"name...", "name"},    // 末尾ドット除去
		{"  hello  ", "hello"}, // 前後空白除去
		{"title . ", "title"},  // 末尾の空白・ドット混在
		{".hidden", ".hidden"}, // 先頭ドットは保持
		{"日本語タイトル", "日本語タイトル"}, // Unicode はそのまま
		{"", ""},
	}
	for _, c := range cases {
		if got := sanitizeFilename(c.in); got != c.want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// 仕様: docs/design.md「uniqueDest について」
// 同名が無ければそのまま、有れば (1)(2)… の連番を付ける。既存ファイルは上書きしない。
func TestUniqueDest(t *testing.T) {
	dir := t.TempDir()

	// 衝突なし
	if got := uniqueDest(dir, "a.mp4"); got != filepath.Join(dir, "a.mp4") {
		t.Errorf("衝突なし: got %q", got)
	}

	// 1 件衝突 → (1)
	mustTouch(t, dir, "a.mp4")
	if got := uniqueDest(dir, "a.mp4"); got != filepath.Join(dir, "a (1).mp4") {
		t.Errorf("1件衝突: got %q, want %q", got, filepath.Join(dir, "a (1).mp4"))
	}

	// 2 件衝突 → (2)
	mustTouch(t, dir, "a (1).mp4")
	if got := uniqueDest(dir, "a.mp4"); got != filepath.Join(dir, "a (2).mp4") {
		t.Errorf("2件衝突: got %q, want %q", got, filepath.Join(dir, "a (2).mp4"))
	}

	// 拡張子なし
	mustTouch(t, dir, "README")
	if got := uniqueDest(dir, "README"); got != filepath.Join(dir, "README (1)") {
		t.Errorf("拡張子なし: got %q, want %q", got, filepath.Join(dir, "README (1)"))
	}
}

func mustTouch(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// 仕様: docs/design.md「進捗通知」と parseYtDlpLine のフォーマット
// [download]  45.3% of   10.00MiB at    1.50MiB/s ETA 00:03
func TestParseYtDlpLine(t *testing.T) {
	t.Run("通常の進捗行", func(t *testing.T) {
		var it DownloadItem
		parseYtDlpLine("[download]  45.3% of   10.00MiB at    1.50MiB/s ETA 00:03", &it)
		if it.Percent != 45.3 {
			t.Errorf("Percent = %v, want 45.3", it.Percent)
		}
		if it.TotalSize != "10.00MiB" {
			t.Errorf("TotalSize = %q, want 10.00MiB", it.TotalSize)
		}
		if it.Speed != "1.50MiB/s" {
			t.Errorf("Speed = %q, want 1.50MiB/s", it.Speed)
		}
		if it.ETA != "00:03" {
			t.Errorf("ETA = %q, want 00:03", it.ETA)
		}
		if it.Status != "" {
			t.Errorf("Status = %q, want \"\"（進捗中は変更しない）", it.Status)
		}
	})

	t.Run("100%でも finished にしない（後処理が残るため）", func(t *testing.T) {
		// 進捗 100% はダウンロード完了であって全体の成功ではない。
		// ffmpeg 結合などの後処理が残るため、Status は進捗パースで確定させない。
		// docs/design.md「finished は進捗 100% で決めてはならない」を参照。
		var it DownloadItem
		it.Status = "downloading"
		parseYtDlpLine("[download] 100% of 5.00MiB", &it)
		if it.Percent != 100 {
			t.Errorf("Percent = %v, want 100", it.Percent)
		}
		if it.Status == "finished" {
			t.Errorf("Status を finished にしてはいけない（後処理失敗を握りつぶす）: %q", it.Status)
		}
	})

	t.Run("Destination 行は無視", func(t *testing.T) {
		var it DownloadItem
		parseYtDlpLine("[download] Destination: /tmp/foo.mp4", &it)
		if it.Percent != 0 || it.TotalSize != "" {
			t.Errorf("Destination 行で状態が変わった: %+v", it)
		}
	})

	t.Run("already downloaded は finished", func(t *testing.T) {
		var it DownloadItem
		parseYtDlpLine("[download] foo.mp4 has already been downloaded", &it)
		if it.Status != "finished" || it.Percent != 100 {
			t.Errorf("got Status=%q Percent=%v, want finished/100", it.Status, it.Percent)
		}
	})

	t.Run("download 以外の行は無視", func(t *testing.T) {
		var it DownloadItem
		parseYtDlpLine("[info] Writing video metadata", &it)
		if it.Percent != 0 {
			t.Errorf("非 download 行で変化した: %+v", it)
		}
	})

	t.Run("Unknown は採用しない", func(t *testing.T) {
		var it DownloadItem
		parseYtDlpLine("[download]  10.0% of 1.00MiB at Unknown ETA Unknown", &it)
		if it.TotalSize != "1.00MiB" {
			t.Errorf("TotalSize = %q, want 1.00MiB", it.TotalSize)
		}
		if it.Speed != "" || it.ETA != "" {
			t.Errorf("Unknown を採用した: Speed=%q ETA=%q", it.Speed, it.ETA)
		}
	})

	t.Run("~（推定サイズ）は採用しない", func(t *testing.T) {
		var it DownloadItem
		parseYtDlpLine("[download]  10.0% of ~ 5.00MiB at 1.00MiB/s ETA 00:10", &it)
		if it.TotalSize != "" {
			t.Errorf("~ をサイズとして採用した: %q", it.TotalSize)
		}
	})
}

func TestFormatElapsed(t *testing.T) {
	cases := []struct {
		secs int
		want string
	}{
		{0, "0:00"},
		{5, "0:05"},
		{65, "1:05"},
		{600, "10:00"},
		{3600, "1:00:00"},
		{3661, "1:01:01"},
	}
	for _, c := range cases {
		if got := formatElapsed(time.Duration(c.secs) * time.Second); got != c.want {
			t.Errorf("formatElapsed(%ds) = %q, want %q", c.secs, got, c.want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		secs int
		want string
	}{
		{0, ""},  // 0 以下は空
		{-5, ""}, // 負値も空
		{65, "1:05"},
		{3661, "1:01:01"},
	}
	for _, c := range cases {
		if got := formatDuration(c.secs); got != c.want {
			t.Errorf("formatDuration(%d) = %q, want %q", c.secs, got, c.want)
		}
	}
}
