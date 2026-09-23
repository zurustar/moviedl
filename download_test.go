// download_test.go: download.go の例示ベーステスト。PBT は pbt_test.go に分離している（PBT-10）。

package main

import (
	"strings"
	"testing"
)

// 仕様: aidlc-docs/inception/application-design/design.md「期限切れ URL」
// requirements.md「期限切れ URL」: 403 で失敗したとき失効の可能性と取り直しを案内する。
func TestIsYtDlpErrorLine(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"ERROR: [generic] master: Unable to download webpage: HTTP Error 403: Forbidden", true},
		{"ERROR: fragment 2 not found, unable to continue", true},
		{"  ERROR: something failed  ", true}, // 前後空白はトリムされる
		{"[download]  45.3% of   10.00MiB", false},
		{"[hlsnative] Total fragments: 3", false},
		{"WARNING: something", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isYtDlpErrorLine(c.in); got != c.want {
			t.Errorf("isYtDlpErrorLine(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestExplainDownloadError(t *testing.T) {
	const m3u8 = "https://vod.e.com/hls/x/master.m3u8?token=SECRETTOKEN"
	const normal = "https://e.com/watch?v=abc"

	t.Run("m3u8 の 403 は URL 失効の可能性を案内する", func(t *testing.T) {
		got := explainDownloadError("ERROR: Unable to download webpage: HTTP Error 403: Forbidden", m3u8, "exit status 1")
		for _, want := range []string{"403", "失効", "取り直"} {
			if !strings.Contains(got, want) {
				t.Errorf("説明 %q に %q が含まれない", got, want)
			}
		}
	})

	t.Run("m3u8 でない 403 は失効の案内をしない", func(t *testing.T) {
		got := explainDownloadError("ERROR: HTTP Error 403: Forbidden", normal, "exit status 1")
		if !strings.Contains(got, "403") {
			t.Errorf("説明 %q に 403 が含まれない", got)
		}
		if strings.Contains(got, "失効") {
			t.Errorf("m3u8 でないのに失効を案内している: %q", got)
		}
	})

	t.Run("403 以外のエラー行はそのまま伝える", func(t *testing.T) {
		got := explainDownloadError("ERROR: fragment 2 not found, unable to continue", m3u8, "exit status 1")
		if !strings.Contains(got, "fragment 2 not found") {
			t.Errorf("説明 %q に元のエラーが含まれない", got)
		}
	})

	t.Run("エラー行がなければ終了状態を使う", func(t *testing.T) {
		got := explainDownloadError("", normal, "exit status 1")
		if got != "exit status 1" {
			t.Errorf("説明 = %q, want %q", got, "exit status 1")
		}
	})

	// SECURITY-03: ユーザー向け文言にもトークンを載せない（画面・クリップボード経由で漏れる）。
	t.Run("説明にトークンが含まれない", func(t *testing.T) {
		const secret = "SECRETTOKEN"
		for _, line := range []string{
			"ERROR: Unable to download webpage: HTTP Error 403: Forbidden (" + m3u8 + ")",
			"ERROR: unable to fetch https://cdn.e.com/seg.ts?sig=" + secret,
			"",
		} {
			if got := explainDownloadError(line, m3u8, "exit status 1"); strings.Contains(got, secret) {
				t.Errorf("説明 %q にトークンが残っている", got)
			}
		}
	})
}

// 仕様: aidlc-docs/inception/application-design/design.md「バージョン情報の埋め込み」
// リリースはタグをそのまま、dev のときだけビルド日を併記。
// 仕様: aidlc-docs/inception/application-design/design.md「ダウンロードの堅牢化」「フォーマット選択」
func TestBuildYtDlpArgs(t *testing.T) {
	join := func(a []string) string { return strings.Join(a, " ") }

	t.Run("堅牢化オプションは ffmpeg 有無に関わらず常に付く", func(t *testing.T) {
		for _, ff := range []string{"", "/usr/bin/ffmpeg"} {
			got := join(buildYtDlpArgs("abc", "/work", ff, "https://e.com/v", ""))
			for _, want := range []string{
				"--abort-on-unavailable-fragment",
				"--fragment-retries 10",
				"--retries 10",
				"--socket-timeout 30",
			} {
				if !strings.Contains(got, want) {
					t.Errorf("ffmpegLoc=%q: args %q に %q がない", ff, got, want)
				}
			}
		}
	})

	t.Run("ffmpeg あり → 映像+音声の最高画質を mp4 結合", func(t *testing.T) {
		s := join(buildYtDlpArgs("abc", "/work", "/usr/bin/ffmpeg", "https://e.com/v", ""))
		for _, want := range []string{
			"--ffmpeg-location /usr/bin/ffmpeg",
			"-f bestvideo+bestaudio/best",
			"--merge-output-format mp4",
		} {
			if !strings.Contains(s, want) {
				t.Errorf("args %q に %q がない", s, want)
			}
		}
	})

	t.Run("ffmpeg なし → 単一フォーマットにフォールバックし結合しない", func(t *testing.T) {
		s := join(buildYtDlpArgs("abc", "/work", "", "https://e.com/v", ""))
		if !strings.Contains(s, "-f best[ext=mp4]/best") {
			t.Errorf("フォールバック書式がない: %q", s)
		}
		if strings.Contains(s, "--merge-output-format") {
			t.Errorf("ffmpeg なしで結合してはいけない: %q", s)
		}
	})

	t.Run("URL は -- 区切りの直後で末尾", func(t *testing.T) {
		got := buildYtDlpArgs("abc", "/work", "", "https://e.com/v", "")
		if got[len(got)-1] != "https://e.com/v" {
			t.Errorf("URL が末尾でない: %v", got)
		}
		if got[len(got)-2] != "--" {
			t.Errorf("URL の直前が -- でない: %v", got)
		}
	})

	// 仕様: design.md「Referer は m3u8 URL に限って付与する」
	t.Run("m3u8 URL には --referer でオリジンを渡す", func(t *testing.T) {
		for _, ff := range []string{"", "/usr/bin/ffmpeg"} {
			s := join(buildYtDlpArgs("abc", "/work", ff, "https://vod.e.com/hls/x/master.m3u8?t=1", ""))
			if !strings.Contains(s, "--referer https://vod.e.com/") {
				t.Errorf("ffmpegLoc=%q: --referer がない: %q", ff, s)
			}
		}
	})

	// デグレ防止の表明: 既存の対応サイトは Referer なしで動いているため、
	// m3u8 以外の URL に --referer を付けてはいけない。
	t.Run("m3u8 でない URL には --referer を付けない", func(t *testing.T) {
		for _, u := range []string{
			"https://e.com/v",
			"https://e.com/watch?v=abc",
			"https://e.com/video.mp4",
		} {
			s := join(buildYtDlpArgs("abc", "/work", "/usr/bin/ffmpeg", u, ""))
			if strings.Contains(s, "--referer") {
				t.Errorf("url=%q に --referer を付けてはいけない: %q", u, s)
			}
		}
	})

	// 仕様: design.md「Referer のユーザー指定（元ページ URL）」
	t.Run("ユーザー指定の元ページ URL を --referer に渡す", func(t *testing.T) {
		const page = "https://www.example.com/video/123/"
		s := join(buildYtDlpArgs("abc", "/work", "", "https://cdn.other.net/hls/x/master.m3u8", page))
		if !strings.Contains(s, "--referer "+page) {
			t.Errorf("指定した元ページ URL が渡っていない: %q", s)
		}
		// 自動導出（m3u8 のオリジン）は使われない
		if strings.Contains(s, "--referer https://cdn.other.net/") {
			t.Errorf("自動導出がユーザー指定に勝ってしまっている: %q", s)
		}
	})

	t.Run("m3u8 でない URL でもユーザー指定は渡す", func(t *testing.T) {
		const page = "https://www.example.com/video/123/"
		s := join(buildYtDlpArgs("abc", "/work", "", "https://e.com/watch?v=abc", page))
		if !strings.Contains(s, "--referer "+page) {
			t.Errorf("指定した元ページ URL が渡っていない: %q", s)
		}
	})

	t.Run("不正な指定は渡さない", func(t *testing.T) {
		s := join(buildYtDlpArgs("abc", "/work", "", "https://e.com/watch?v=abc", "--referer=http://evil/"))
		if strings.Contains(s, "--referer") {
			t.Errorf("不正な指定を渡してしまった: %q", s)
		}
	})

	t.Run("--referer は -- 終端より前に置く", func(t *testing.T) {
		got := buildYtDlpArgs("abc", "/work", "", "https://e.com/master.m3u8", "")
		refIdx, sepIdx := -1, -1
		for i, a := range got {
			if a == "--referer" {
				refIdx = i
			}
			if a == "--" {
				sepIdx = i
			}
		}
		if refIdx == -1 || sepIdx == -1 {
			t.Fatalf("--referer または -- が見つからない: %v", got)
		}
		if refIdx > sepIdx {
			t.Errorf("--referer が -- より後ろにある（位置引数に化ける）: %v", got)
		}
	})

	t.Run("出力テンプレートと作業ディレクトリ指定", func(t *testing.T) {
		s := join(buildYtDlpArgs("abc123", "/work", "", "https://e.com/v", ""))
		if !strings.Contains(s, "-o abc123.%(ext)s") {
			t.Errorf("出力テンプレートがない: %q", s)
		}
		if !strings.Contains(s, "-P home:/work") || !strings.Contains(s, "-P temp:/work") {
			t.Errorf("-P 指定がない: %q", s)
		}
	})
}

// 仕様: design.md「yt-dlp の更新（UpdateYtDlp）」
// runDownload 終了時にリストから取り除くべき状態を判定する。
// error は「リトライ・削除できるよう残す」、queued は「更新のため停止して
// 待ちキューへ戻したものが再開できるよう残す」。どちらも消してはいけない。
func TestShouldRemoveWhenDone(t *testing.T) {
	cases := []struct {
		status string
		want   bool
	}{
		{"finished", true},
		{"cancelled", true},
		{"error", false},  // リトライ・URL コピーのために残す
		{"queued", false}, // 更新のため停止して再キューしたアイテムを消さない
	}
	for _, c := range cases {
		if got := shouldRemoveWhenDone(c.status); got != c.want {
			t.Errorf("shouldRemoveWhenDone(%q) = %v, want %v", c.status, got, c.want)
		}
	}
}
