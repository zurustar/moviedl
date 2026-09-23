// urls_test.go: urls.go の例示ベーステスト。PBT は pbt_test.go に分離している（PBT-10）。

package main

import (
	"strings"
	"testing"
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

// 仕様: aidlc-docs/inception/application-design/design.md「Referer のユーザー指定（元ページ URL）」
// 自動導出は m3u8 自身のオリジンなので、プレイヤーが別ドメインにある構成では効かない。
// ユーザーが指定した元ページ URL を優先する。
func TestEffectiveReferer(t *testing.T) {
	const m3u8 = "https://cdn.example.net/hls/x/master.m3u8?t=1"
	const normal = "https://e.com/watch?v=abc"
	const page = "https://www.example.com/video/123/"

	cases := []struct {
		name     string
		url      string
		override string
		want     string
	}{
		{
			name: "指定なし + m3u8 → 自動導出（m3u8 のオリジン）",
			url:  m3u8, override: "", want: "https://cdn.example.net/",
		},
		{
			name: "指定あり → 指定を優先する（自動導出に勝つ）",
			url:  m3u8, override: page, want: page,
		},
		{
			// 自動付与を m3u8 に限定したのは既存経路を変えないため。
			// ユーザーが明示したものはその限定の対象外。
			name: "指定あり + m3u8 でない URL → 指定を使う",
			url:  normal, override: page, want: page,
		},
		{
			name: "指定なし + m3u8 でない URL → 何も渡さない",
			url:  normal, override: "", want: "",
		},
		{
			name: "空白だけの指定は指定なし扱い",
			url:  m3u8, override: "   ", want: "https://cdn.example.net/",
		},
		{
			// 不正な指定は無視して自動導出に落ちる（多層防御）。
			name: "不正な指定は無視する（m3u8 → 自動導出）",
			url:  m3u8, override: "--referer=http://evil/", want: "https://cdn.example.net/",
		},
		{
			name: "不正な指定は無視する（m3u8 でない → 空）",
			url:  normal, override: "not-a-url", want: "",
		},
		{
			name: "file スキームの指定は無視する",
			url:  normal, override: "file:///etc/passwd", want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := effectiveReferer(c.url, c.override); got != c.want {
				t.Errorf("effectiveReferer(%q, %q) = %q, want %q", c.url, c.override, got, c.want)
			}
		})
	}

	// 引数インジェクション対策: 任意の指定値に対して "-" 始まりを返さない。
	t.Run("'-' 始まりを返さない", func(t *testing.T) {
		for _, ov := range []string{
			"-J", "--exec=touch /tmp/x", "--referer=http://evil/", "-o-", "", "   ",
			"https://ok.example.com/", "file:///x",
		} {
			for _, u := range []string{m3u8, normal, "", "-J"} {
				if got := effectiveReferer(u, ov); strings.HasPrefix(got, "-") {
					t.Errorf("effectiveReferer(%q, %q) = %q: '-' 始まりは yt-dlp のオプションに化ける", u, ov, got)
				}
			}
		}
	})
}
