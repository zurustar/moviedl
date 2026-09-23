// logging_test.go: logging.go の例示ベーステスト。PBT は pbt_test.go に分離している（PBT-10）。

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 仕様: aidlc-docs/inception/application-design/design.md「ログにトークン付き URL を残さない（redactLine）」
// m3u8 の URL は有効期限トークンが実質的な認可情報なので、平文ログに残してはいけない（SECURITY-03）。
func TestRedactLine(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "クエリはマスクし、パスは残す",
			in:   "[STEP2] command: yt-dlp -- https://e.com/hls/a/master.m3u8?token=SECRETVALUE",
			want: "[STEP2] command: yt-dlp -- https://e.com/hls/a/master.m3u8?<redacted>",
		},
		{
			name: "複数パラメータもまとめてマスクする",
			in:   "https://e.com/a.m3u8?token=SECRET&exp=1789&sig=DEADBEEF",
			want: "https://e.com/a.m3u8?<redacted>",
		},
		{
			name: "同じ行の複数 URL をすべてマスクする",
			in:   "from https://a.com/x.m3u8?k=S1 to https://b.com/y.ts?k=S2 done",
			want: "from https://a.com/x.m3u8?<redacted> to https://b.com/y.ts?<redacted> done",
		},
		{
			name: "クエリがない URL は変化しない",
			in:   "--referer https://e.com/ -- https://e.com/hls/a/master.m3u8",
			want: "--referer https://e.com/ -- https://e.com/hls/a/master.m3u8",
		},
		{
			name: "URL を含まない行は一切変化しない",
			in:   "[download]  45.3% of   10.00MiB at    1.50MiB/s ETA 00:03",
			want: "[download]  45.3% of   10.00MiB at    1.50MiB/s ETA 00:03",
		},
		{
			name: "http も対象",
			in:   "http://e.com/a.m3u8?t=S",
			want: "http://e.com/a.m3u8?<redacted>",
		},
		{
			name: "引用符で囲まれた URL の外側は壊さない",
			in:   `Destination: "https://e.com/a.m3u8?t=S"`,
			want: `Destination: "https://e.com/a.m3u8?<redacted>"`,
		},
		{
			name: "空文字",
			in:   "",
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := redactLine(c.in); got != c.want {
				t.Errorf("redactLine(%q)\n got = %q\nwant = %q", c.in, got, c.want)
			}
		})
	}

	// 冪等であること（ログ経路が増えて二重適用されても壊れない）。
	t.Run("冪等", func(t *testing.T) {
		for _, in := range []string{
			"https://e.com/a.m3u8?token=SECRET",
			"https://e.com/a.m3u8",
			"no url here",
			"a https://x/y?z=1 b https://p/q?r=2 c",
		} {
			once := redactLine(in)
			if twice := redactLine(once); twice != once {
				t.Errorf("redactLine が冪等でない: in=%q once=%q twice=%q", in, once, twice)
			}
		}
	})

	// トークンそのものが出力に残らないこと。
	t.Run("トークンが出力に残らない", func(t *testing.T) {
		const secret = "SUPERSECRETTOKEN"
		for _, in := range []string{
			"https://e.com/a.m3u8?token=" + secret,
			"cmd -- https://e.com/a.m3u8?a=1&token=" + secret + "&b=2",
			"https://e.com/seg.ts?sig=" + secret,
		} {
			if got := redactLine(in); strings.Contains(got, secret) {
				t.Errorf("redactLine(%q) = %q にトークンが残っている", in, got)
			}
		}
	})
}

// 仕様: aidlc-docs/inception/application-design/design.md「ログにトークン付き URL を残さない（redactLine）」
// マスクはログ行の組み立てで行う。各呼び出し側が redactLine を呼ぶ設計にすると
// 1 箇所でも漏れたらその経路だけ穴が空くため、構造的に漏れない形にする。
func TestLogLine(t *testing.T) {
	now := time.Date(2026, 9, 21, 9, 15, 30, 500_000_000, time.UTC)

	t.Run("タイムスタンプ接頭辞と改行が付く", func(t *testing.T) {
		got := logLine(now, "[STEP1] workDir created")
		want := "[09:15:30.500] [STEP1] workDir created\n"
		if got != want {
			t.Errorf("logLine() = %q, want %q", got, want)
		}
	})

	t.Run("引数に含まれる URL のクエリは組み立て時にマスクされる", func(t *testing.T) {
		const secret = "SUPERSECRETTOKEN"
		got := logLine(now, "[STEP2] command: %s %s", "yt-dlp", "-- https://e.com/a/master.m3u8?token="+secret)
		if strings.Contains(got, secret) {
			t.Errorf("logLine() にトークンが残っている: %q", got)
		}
		if !strings.Contains(got, "https://e.com/a/master.m3u8?<redacted>") {
			t.Errorf("logLine() のマスク結果が想定と違う: %q", got)
		}
	})

	t.Run("yt-dlp の出力行に含まれる URL もマスクされる", func(t *testing.T) {
		const secret = "FRAGTOKEN"
		got := logLine(now, "[STEP3] yt-dlp: %s", "[download] Got error: HTTP Error 403 for https://cdn.e.com/seg001.ts?sig="+secret)
		if strings.Contains(got, secret) {
			t.Errorf("logLine() にトークンが残っている: %q", got)
		}
	})

	t.Run("URL を含まない行は書式以外変化しない", func(t *testing.T) {
		got := logLine(now, "[STEP3] yt-dlp: %s", "[download]  45.3% of   10.00MiB")
		want := "[09:15:30.500] [STEP3] yt-dlp: [download]  45.3% of   10.00MiB\n"
		if got != want {
			t.Errorf("logLine() = %q, want %q", got, want)
		}
	})
}

// useTempLog はログ出力先を一時ディレクトリへ向ける。
// テストが実ログ（~/Library/Application Support/moviedl/moviedl.log）へ
// 書き込むと、調査したい本物の記録をテストのノイズで汚してしまうため。
func useTempLog(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev := logDirOverride
	logDirOverride = dir
	t.Cleanup(func() { logDirOverride = prev })
	return filepath.Join(dir, "moviedl.log")
}

// 仕様: aidlc-docs/inception/application-design/design.md「ログのセッション管理」
// 起動時にログを消してはならない（再現後に再起動すると証拠が失われる）。
func TestShouldRotateLog(t *testing.T) {
	cases := []struct {
		size int64
		want bool
	}{
		{0, false},
		{1, false},
		{maxLogBytes - 1, false},
		{maxLogBytes, true},
		{maxLogBytes + 1, true},
		{100 << 20, true},
	}
	for _, c := range cases {
		if got := shouldRotateLog(c.size); got != c.want {
			t.Errorf("shouldRotateLog(%d) = %v, want %v", c.size, got, c.want)
		}
	}
}

func TestStartLogSessionKeepsPreviousRun(t *testing.T) {
	p := useTempLog(t)

	t.Run("前回の内容を消さずに追記する", func(t *testing.T) {
		if err := os.WriteFile(p, []byte("[00:00:00.000] 前回のセッションの記録\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		startLogSession()

		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		s := string(b)
		if !strings.Contains(s, "前回のセッションの記録") {
			t.Error("起動で前回のログが消えた（再現後に再起動すると証拠が失われる）")
		}
		if !strings.Contains(s, "session start") {
			t.Errorf("セッション開始行がない: %q", s)
		}
	})

	t.Run("上限を超えたら 1 世代退避してから始める", func(t *testing.T) {
		big := make([]byte, maxLogBytes+10)
		for i := range big {
			big[i] = 'x'
		}
		if err := os.WriteFile(p, big, 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		startLogSession()

		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if fi.Size() >= maxLogBytes {
			t.Errorf("上限超過後もログが切り替わっていない: %d バイト", fi.Size())
		}
		if _, err := os.Stat(p + ".1"); err != nil {
			t.Errorf("退避世代 %s が作られていない: %v", p+".1", err)
		}
	})
}

func TestAppendLog(t *testing.T) {
	p := useTempLog(t)

	appendLog("[TEST] 1 行目 %d", 1)
	appendLog("[TEST] 2 行目 url=%s", "https://e.com/a.m3u8?token=SUPERSECRET")

	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, "1 行目 1") || !strings.Contains(s, "2 行目") {
		t.Errorf("追記されていない: %q", s)
	}
	// appendLog も logLine 経由でマスクされること（SECURITY-03）。
	if strings.Contains(s, "SUPERSECRET") {
		t.Errorf("appendLog がトークンをマスクしていない: %q", s)
	}
	if !strings.Contains(s, redactedQuery) {
		t.Errorf("マスク結果が見当たらない: %q", s)
	}
}
