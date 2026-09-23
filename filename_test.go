// filename_test.go: filename.go の例示ベーステスト。PBT は pbt_test.go に分離している（PBT-10）。

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

func mustTouch(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// 仕様: aidlc-docs/inception/application-design/design.md「(1) サフィックス問題」
// id が "-1" で終わり title が " (1)" で終わる場合のみ " (1)" を除去。
func TestStripDedupSuffix(t *testing.T) {
	cases := []struct {
		id, title, want string
	}{
		{"abc-1", "My Video (1)", "My Video"},                   // dedup アーティファクト → 除去
		{"abc", "My Video (1)", "My Video (1)"},                 // id が -1 でない → 保持
		{"abc-1", "My Video", "My Video"},                       // title が (1) でない → そのまま
		{"abc-2", "Part (1)", "Part (1)"},                       // -1 でない → 保持
		{"abc-1", "Real Title (1) here", "Real Title (1) here"}, // 末尾でない (1) → 保持
	}
	for _, c := range cases {
		if got := stripDedupSuffix(c.id, c.title); got != c.want {
			t.Errorf("stripDedupSuffix(%q,%q) = %q, want %q", c.id, c.title, got, c.want)
		}
	}
}
