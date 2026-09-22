package main

import (
	"os"
	"path/filepath"
	"strings"
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

// 仕様: aidlc-docs/inception/application-design/design.md「判定は 1 つの述語に集約する（isM3U8URL）」
// パスが .m3u8 で終わるかを判定する。クエリ文字列は判定に含めない。
func TestIsM3U8URL(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		// 正常系
		{"https://vod.example.com/hls/ab12cd/master.m3u8", true},
		{"http://example.com/media.m3u8", true},
		{"  https://example.com/a.m3u8  ", true}, // 前後空白はトリムされる

		// クエリ付きでも判定は変わらない（生文字列の HasSuffix では落ちる）
		{"https://example.com/master.m3u8?token=abc123", true},
		{"https://example.com/master.m3u8?a=1&b=2", true},

		// 大小無視
		{"https://example.com/MASTER.M3U8", true},
		{"https://example.com/Master.M3u8", true},

		// m3u8 ではない
		{"https://example.com/video.mp4", false},
		{"https://example.com/watch?v=abc", false},
		{"https://example.com/", false},

		// .m3u8 がパス末尾でない箇所に現れるだけのもの
		{"https://m3u8.example.com/video.mp4", false},
		{"https://example.com/a.m3u8/b.mp4", false},
		{"https://example.com/x?file=a.m3u8", false},

		// 不正な URL
		{"--exec=touch /tmp/pwned.m3u8", false},
		{"file:///tmp/a.m3u8", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isM3U8URL(c.in); got != c.want {
			t.Errorf("isM3U8URL(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// 仕様: aidlc-docs/inception/application-design/design.md「Referer は m3u8 URL に限って付与する」
// m3u8 URL のオリジン（scheme://host/）を返す。m3u8 でない・不正な URL なら "" を返す。
func TestRefererFor(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// 正常系: オリジンだけを返す（パス・クエリ・フラグメントは落とす）
		{"https://vod.example.com/hls/ab12cd/master.m3u8", "https://vod.example.com/"},
		{"http://example.com/media.m3u8", "http://example.com/"},
		{"https://example.com/master.m3u8?token=abc123", "https://example.com/"},
		{"https://example.com/a/b/c.m3u8#frag", "https://example.com/"},
		{"  https://example.com/a.m3u8  ", "https://example.com/"},

		// ポートは保つ（別ポートは別オリジン）
		{"http://example.com:8080/media.m3u8", "http://example.com:8080/"},

		// m3u8 でないものには付与しない（既存サイトの経路に触らないため）
		{"https://example.com/video.mp4", ""},
		{"https://example.com/watch?v=abc", ""},

		// 不正な URL
		{"file:///tmp/a.m3u8", ""},
		{"--referer=http://evil/", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := refererFor(c.in); got != c.want {
			t.Errorf("refererFor(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	// 引数インジェクション対策: 戻り値は決して "-" で始まらない。
	// design.md「引数インジェクション対策（-- 終端は必須）」と同じ多層防御。
	for _, in := range []string{
		"https://example.com/a.m3u8",
		"--exec=x.m3u8",
		"-J",
		"http://-evil.example.com/a.m3u8",
		"",
	} {
		if got := refererFor(in); strings.HasPrefix(got, "-") {
			t.Errorf("refererFor(%q) = %q: '-' 始まりを返してはいけない", in, got)
		}
	}
}

// 仕様: aidlc-docs/inception/application-design/design.md「保存名は URL だけから組み立てる（m3u8FileName）」
// HLS にはタイトルのメタデータがないため、URL から判別可能な base 名を組み立てる。
func TestM3U8FileName(t *testing.T) {
	now := time.Date(2026, 9, 21, 9, 15, 0, 0, time.UTC)
	const ts = "20260921-0915"

	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "ホスト + 意味のあるパス要素 + 日時",
			in:   "https://vod.example.com/hls/ab12cd/master.m3u8",
			want: "vod.example.com_ab12cd_" + ts,
		},
		{
			name: "パス要素が汎用語だけなら省略する",
			in:   "https://example.com/hls/master.m3u8",
			want: "example.com_" + ts,
		},
		{
			name: "パス要素がなければ省略する",
			in:   "https://example.com/master.m3u8",
			want: "example.com_" + ts,
		},
		{
			name: "末尾から汎用語を飛ばして最初の非汎用語を採る",
			in:   "https://example.com/abc123/hls/stream/index.m3u8",
			want: "example.com_abc123_" + ts,
		},
		{
			name: "クエリのトークンは保存名に含めない",
			in:   "https://example.com/v/xyz789/master.m3u8?token=SECRETTOKEN&exp=99",
			want: "example.com_xyz789_" + ts,
		},
		{
			name: "ポートは保存名に含めない（ファイル名に : を持ち込まない）",
			in:   "https://example.com:8080/abc/a.m3u8",
			want: "example.com_abc_" + ts,
		},
		{
			name: "m3u8 でない URL には空文字を返す",
			in:   "https://example.com/video.mp4",
			want: "",
		},
		{
			name: "不正な URL には空文字を返す",
			in:   "--exec=x.m3u8",
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := m3u8FileName(c.in, now); got != c.want {
				t.Errorf("m3u8FileName(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}

	// 生成名はそのままファイル名として使える（sanitizeFilename が何も変えない）こと。
	t.Run("生成名は既に安全なファイル名である", func(t *testing.T) {
		for _, in := range []string{
			"https://example.com/abc/a.m3u8",
			"https://example.com:8080/a:b/c*d/a.m3u8",
			"https://example.com/../a.m3u8",
			"https://example.com/CON/a.m3u8",
		} {
			got := m3u8FileName(in, now)
			if got == "" {
				t.Errorf("m3u8FileName(%q) が空文字（保存名が決まらない）", in)
				continue
			}
			if s := sanitizeFilename(got); s != got {
				t.Errorf("m3u8FileName(%q) = %q は sanitizeFilename で %q に変わる（既に安全であるべき）", in, got, s)
			}
			if strings.ContainsAny(got, `/\:`) {
				t.Errorf("m3u8FileName(%q) = %q にパス区切りが含まれる", in, got)
			}
		}
	})
}

// 仕様: aidlc-docs/inception/application-design/design.md「適用箇所」
// m3u8 URL は STEP2a で取得したタイトルを使わず、URL から生成した名前を使う。
func TestResolveTitle(t *testing.T) {
	now := time.Date(2026, 9, 21, 9, 15, 0, 0, time.UTC)
	const ts = "20260921-0915"

	cases := []struct {
		name         string
		url          string
		fetchedTitle string
		want         string
	}{
		{
			name:         "m3u8 は取得タイトル（= m3u8 のファイル名）を捨てて生成名を使う",
			url:          "https://vod.example.com/hls/ab12cd/master.m3u8",
			fetchedTitle: "master",
			want:         "vod.example.com_ab12cd_" + ts,
		},
		{
			name:         "m3u8 でタイトルが取れなくても生成名が決まる",
			url:          "https://vod.example.com/hls/ab12cd/master.m3u8",
			fetchedTitle: "",
			want:         "vod.example.com_ab12cd_" + ts,
		},
		{
			// デグレ防止: 既存サイトのタイトルを壊してはいけない
			name:         "m3u8 でない URL は取得タイトルをそのまま使う",
			url:          "https://example.com/watch?v=abc",
			fetchedTitle: "Never Gonna Give You Up",
			want:         "Never Gonna Give You Up",
		},
		{
			name:         "m3u8 でなくタイトルも空ならそのまま空（既存の tmpBase フォールバック経路）",
			url:          "https://example.com/watch?v=abc",
			fetchedTitle: "",
			want:         "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveTitle(c.url, c.fetchedTitle, now); got != c.want {
				t.Errorf("resolveTitle(%q, %q) = %q, want %q", c.url, c.fetchedTitle, got, c.want)
			}
		})
	}
}

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

// 仕様: aidlc-docs/inception/application-design/design.md「Kill(-pid) の符号は致命的に危険」
//
// kill(2) は第 1 引数の符号と値で意味が変わる。-pgid でグループ全体を狙うとき、
// pid が 0 や 1 だと kill(0, ...)（自分自身のグループ）や kill(-1, ...)（権限内の全プロセス）
// が成立してアプリごと巻き込んで殺す。符号を反転させる前にこの述語で必ず弾く。
func TestIsKillablePID(t *testing.T) {
	cases := []struct {
		pid  int
		want bool
	}{
		// 正常系: 通常のプロセス
		{2, true},
		{3, true},
		{12345, true},
		{1 << 20, true},

		// 弾かなければならない値
		{1, false},  // init / launchd。-1 に反転すると全プロセスへの送信になる
		{0, false},  // -0 == 0 で自分自身のプロセスグループを殺す
		{-1, false}, // 既に負。反転すると 1 になり init を狙う
		{-2, false},
		{-12345, false},
	}
	for _, c := range cases {
		if got := isKillablePID(c.pid); got != c.want {
			t.Errorf("isKillablePID(%d) = %v, want %v", c.pid, got, c.want)
		}
	}
}

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

// 仕様: aidlc-docs/inception/application-design/design.md「sanitizeFilename について」
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

		// 制御文字（C0 + DEL）はリモートタイトル由来の混入を除去する
		{"a\x00b\nc\td", "abcd"},
		{"line1\r\nline2", "line1line2"},
		{"tab\x7fdel", "tabdel"},

		// Windows 予約デバイス名は先頭に _ を付けて回避する（大小無視・拡張子付きも対象）
		{"CON", "_CON"},
		{"con.mp4", "_con.mp4"},
		{"COM1.txt", "_COM1.txt"},
		{"NUL", "_NUL"},
		{"LPT9", "_LPT9"},
		// 予約名でないものは変更しない
		{"CONTACT", "CONTACT"},
		{"COM0", "COM0"},   // COM1〜9 のみが予約
		{"COM10", "COM10"}, // 2 桁は予約でない
	}
	for _, c := range cases {
		if got := sanitizeFilename(c.in); got != c.want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// 仕様: aidlc-docs/inception/application-design/design.md「uniqueDest について」
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

// 仕様: uniqueDest はパスを返すだけでなく、O_CREATE|O_EXCL でその名前を
// アトミックに予約する。touch せずに同名で 2 回呼んでも別パスを返すこと
// （並行ダウンロードの TOCTOU で同じ名前を選んでしまう競合を防ぐ）。
// aidlc-docs/inception/application-design/design.md「uniqueDest について」参照。
func TestUniqueDestReservesAtomically(t *testing.T) {
	dir := t.TempDir()
	a := uniqueDest(dir, "x.mp4")
	b := uniqueDest(dir, "x.mp4") // 手動 touch せずに 2 回目
	if a == b {
		t.Fatalf("同一パスを 2 度返した（予約されていない）: %q", a)
	}
	if a != filepath.Join(dir, "x.mp4") {
		t.Errorf("1回目 = %q, want %q", a, filepath.Join(dir, "x.mp4"))
	}
	if b != filepath.Join(dir, "x (1).mp4") {
		t.Errorf("2回目 = %q, want %q", b, filepath.Join(dir, "x (1).mp4"))
	}
	// 予約はプレースホルダーとして実体が作られている
	if _, err := os.Stat(a); err != nil {
		t.Errorf("予約パスが作成されていない: %v", err)
	}
}

// 仕様: isManagedWorkDir は basename が ".moviedl-work-" 始まりのパスのみ true。
// cleanupLeftoverWorkDirs の os.RemoveAll はこのガードを通過したものだけ削除し、
// 改竄・破損したレジストリで任意ディレクトリを消さない。
// aidlc-docs/inception/application-design/design.md「workDir 削除はプレフィックス検証必須」参照。
func TestIsManagedWorkDir(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"/home/u/Downloads/.moviedl-work-1a2b3c", true},
		{".moviedl-work-xyz", true},
		{"/home/u/Downloads", false},
		{"/", false},
		{"/home/u/.moviedl-work", false}, // 末尾ダッシュなし → プレフィックス不一致
		{"/home/u/Movies", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isManagedWorkDir(c.in); got != c.want {
			t.Errorf("isManagedWorkDir(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func mustTouch(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// 仕様: aidlc-docs/inception/application-design/design.md「進捗通知」と parseYtDlpLine のフォーマット
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
		// aidlc-docs/inception/application-design/design.md「finished は進捗 100% で決めてはならない」を参照。
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

// 仕様: aidlc-docs/inception/application-design/design.md「バージョン情報の埋め込み」
// リリースはタグをそのまま、dev のときだけビルド日を併記。
// 仕様: aidlc-docs/inception/application-design/design.md「ダウンロードの堅牢化」「フォーマット選択」
func TestBuildYtDlpArgs(t *testing.T) {
	join := func(a []string) string { return strings.Join(a, " ") }

	t.Run("堅牢化オプションは ffmpeg 有無に関わらず常に付く", func(t *testing.T) {
		for _, ff := range []string{"", "/usr/bin/ffmpeg"} {
			got := join(buildYtDlpArgs("abc", "/work", ff, "https://e.com/v"))
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
		s := join(buildYtDlpArgs("abc", "/work", "/usr/bin/ffmpeg", "https://e.com/v"))
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
		s := join(buildYtDlpArgs("abc", "/work", "", "https://e.com/v"))
		if !strings.Contains(s, "-f best[ext=mp4]/best") {
			t.Errorf("フォールバック書式がない: %q", s)
		}
		if strings.Contains(s, "--merge-output-format") {
			t.Errorf("ffmpeg なしで結合してはいけない: %q", s)
		}
	})

	t.Run("URL は -- 区切りの直後で末尾", func(t *testing.T) {
		got := buildYtDlpArgs("abc", "/work", "", "https://e.com/v")
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
			s := join(buildYtDlpArgs("abc", "/work", ff, "https://vod.e.com/hls/x/master.m3u8?t=1"))
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
			s := join(buildYtDlpArgs("abc", "/work", "/usr/bin/ffmpeg", u))
			if strings.Contains(s, "--referer") {
				t.Errorf("url=%q に --referer を付けてはいけない: %q", u, s)
			}
		}
	})

	t.Run("--referer は -- 終端より前に置く", func(t *testing.T) {
		got := buildYtDlpArgs("abc", "/work", "", "https://e.com/master.m3u8")
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
		s := join(buildYtDlpArgs("abc123", "/work", "", "https://e.com/v"))
		if !strings.Contains(s, "-o abc123.%(ext)s") {
			t.Errorf("出力テンプレートがない: %q", s)
		}
		if !strings.Contains(s, "-P home:/work") || !strings.Contains(s, "-P temp:/work") {
			t.Errorf("-P 指定がない: %q", s)
		}
	})
}

func TestFormatVersion(t *testing.T) {
	cases := []struct {
		version, buildDate, want string
	}{
		{"v0.1.8", "2026-05-31", "v0.1.8"},        // リリース: タグのみ
		{"v0.1.8", "", "v0.1.8"},                  // 日付なしでもタグのみ
		{"dev", "2026-05-31", "dev (2026-05-31)"}, // dev: 日付併記
		{"dev", "", "dev"},                        // dev で日付なし
		{"", "", "dev"},                           // 空はフォールバックで dev
	}
	for _, c := range cases {
		if got := formatVersion(c.version, c.buildDate); got != c.want {
			t.Errorf("formatVersion(%q,%q) = %q, want %q", c.version, c.buildDate, got, c.want)
		}
	}
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

// 仕様: aidlc-docs/inception/application-design/design.md「maxActive == 0（登録のみモード）」
// 例示ベーステスト（PBT-10 により pbt_test.go のプロパティテストと併存させる）。
func TestSetMaxConcurrent(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want int
	}{
		{"0 は登録のみモードとして保持する", 0, 0},
		{"負値は 0 にクランプ", -1, 0},
		{"下限より十分小さい負値も 0", -100, 0},
		{"1 はそのまま", 1, 1},
		{"10 はそのまま", 10, 10},
		{"上限超過は 10 にクランプ", 11, 10},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := NewApp()
			a.SetMaxConcurrent(c.in)
			if got := a.GetMaxConcurrent(); got != c.want {
				t.Errorf("SetMaxConcurrent(%d) 後の GetMaxConcurrent() = %d, want %d", c.in, got, c.want)
			}
		})
	}
}

// 起動直後は自動ダウンロードを始めない（既定 0 = 登録のみモード）。
func TestNewAppDefaultsToRegistrationOnly(t *testing.T) {
	if got := NewApp().GetMaxConcurrent(); got != 0 {
		t.Errorf("NewApp() の maxActive = %d, want 0（起動時は登録のみモード）", got)
	}
}
