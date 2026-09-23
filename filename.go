// filename.go: 保存ファイル名の決定（タイトル・m3u8 の生成名・安全化・重複回避）。

package main

import (
	"fmt"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// stripDedupSuffix は yt-dlp の内部 dedup アーティファクト（id が "-1" で終わり
// title が " (1)" で終わる）を検出し、付加された末尾の " (1)" を除去する。
// それ以外は title をそのまま返す。aidlc-docs/inception/application-design/design.md「(1) サフィックス問題」を参照。
func stripDedupSuffix(id, title string) string {
	if strings.HasSuffix(id, "-1") && strings.HasSuffix(title, " (1)") {
		return strings.TrimSuffix(title, " (1)")
	}
	return title
}

// m3u8GenericPathSegments は保存名を組み立てるときに飛ばすパス要素。
// 配信構成上の定型語であって動画を識別しないため、これらを名前に入れても判別に役立たない。
var m3u8GenericPathSegments = map[string]bool{
	"hls": true, "stream": true, "streams": true, "media": true,
	"video": true, "videos": true, "playlist": true, "manifest": true,
	"out": true, "vod": true,
}

// m3u8FileName は m3u8 URL から「ホスト名 + 意味のあるパス要素 + 日時」の拡張子なし base 名を返す。
// m3u8 でない URL には "" を返す。
//
// なぜ必要か: HLS の m3u8 にはタイトルのメタデータがないため、yt-dlp が返す title は
// m3u8 のファイル名そのもの（master / index / playlist）になる。そのままでは保存名が
// master.mp4 に集中し、2 本目以降が uniqueDest によって "master (1).mp4" になって判別できない。
//
// クエリ文字列は含めない。トークンで読めない名前になるうえ、ログのマスク方針と矛盾する。
// 詳細は aidlc-docs/inception/application-design/design.md「保存名は URL だけから組み立てる」を参照。
func m3u8FileName(raw string, now time.Time) string {
	if !isM3U8URL(raw) {
		return ""
	}
	u, err := neturl.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	// Host ではなく Hostname を使う。ポートを含めるとファイル名に ':' が入ってしまう。
	parts := []string{u.Hostname()}
	if seg := meaningfulPathSegment(u.Path); seg != "" {
		parts = append(parts, seg)
	}
	parts = append(parts, now.Format("20060102-1504"))
	// 呼び出し側（STEP5）も sanitizeFilename を通すが、ここで通しておくことで
	// 「m3u8FileName の戻り値はそのままファイル名として使える」を関数の契約にできる。
	// sanitizeFilename は冪等なので二重適用は無害。
	return sanitizeFilename(strings.Join(parts, "_"))
}

// meaningfulPathSegment は .m3u8 のファイル名を除いたパス要素を末尾から走査し、
// 汎用語でない最初の要素を返す。見つからなければ "" を返す。
func meaningfulPathSegment(path string) string {
	segs := strings.Split(path, "/")
	if len(segs) > 0 {
		segs = segs[:len(segs)-1] // 末尾の要素は .m3u8 のファイル名なので捨てる
	}
	for i := len(segs) - 1; i >= 0; i-- {
		s := strings.TrimSpace(segs[i])
		if s == "" || s == "." || s == ".." {
			continue
		}
		if m3u8GenericPathSegments[strings.ToLower(s)] {
			continue
		}
		return s
	}
	return ""
}

// resolveTitle は保存名の元になるタイトルを決める。
// m3u8 URL なら STEP2a で取得したタイトルを捨てて m3u8FileName の生成名を使い、
// それ以外は取得したタイトルをそのまま使う（既存サイトの挙動を変えない）。
//
// なぜ「取得タイトルが汎用語のときだけ上書き」にしないか: パスが .m3u8 で終わる URL は
// generic エクストラクターが処理し、title は必ず m3u8 のファイル名になる（実測）。
// 条件を足すとどちらの名前が使われるかが URL によって変わり、純粋関数で決まらなくなる。
func resolveTitle(url, fetchedTitle string, now time.Time) string {
	if name := m3u8FileName(url, now); name != "" {
		return name
	}
	return fetchedTitle
}

func sanitizeFilename(s string) string {
	// リモートタイトル由来の制御文字（C0 制御 + DEL）を除去する。改行などが
	// ファイル名に混入すると保存失敗やログ汚染の原因になる。
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	r := strings.NewReplacer(`\`, "_", `/`, "_", `:`, "_", `*`, "_", `?`, "_", `"`, "_", `<`, "_", `>`, "_", `|`, "_")
	s = r.Replace(s)
	s = strings.TrimRight(strings.TrimSpace(s), ". ")
	// Windows 予約デバイス名（CON, NUL, COM1〜9 等）はそのままだとファイル作成に
	// 失敗するため、先頭に _ を付けて回避する。
	if isWindowsReservedName(s) {
		s = "_" + s
	}
	return s
}

// isWindowsReservedName は name（拡張子は無視）が Windows の予約デバイス名かを判定する。
// 大文字小文字は区別しない。CON / PRN / AUX / NUL と COM1〜9 / LPT1〜9 が対象。
func isWindowsReservedName(name string) bool {
	base := strings.ToUpper(strings.TrimSuffix(name, filepath.Ext(name)))
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(base) == 4 && base[3] >= '1' && base[3] <= '9' {
		switch base[:3] {
		case "COM", "LPT":
			return true
		}
	}
	return false
}

// uniqueDest は dir 配下に name の衝突しない保存先パスを返す。既存ファイルは
// 決して上書きせず、衝突時は " (1)" " (2)"… の連番を付ける。
//
// 単に Stat で空きを確認して返すのではなく、O_CREATE|O_EXCL で 0 バイトの
// プレースホルダーを作って名前をアトミックに予約する。これにより「空きを
// 確認してから rename するまで」の TOCTOU 窓（並行ダウンロードで同じタイトルを
// 同時取得した際に同名を選んでしまう競合や、外部プロセスが割り込む競合）を閉じる。
// 呼び出し側は返ったパスへ実体を os.Rename する（os.Rename は Unix/Windows とも
// このプレースホルダーを置換する）。rename に失敗した場合は os.Remove で後始末する。
// aidlc-docs/inception/application-design/design.md「uniqueDest について」参照。
func uniqueDest(dir, name string) string {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 0; ; i++ {
		candidate := filepath.Join(dir, name)
		if i > 0 {
			candidate = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", base, i, ext))
		}
		f, err := os.OpenFile(candidate, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			f.Close() //nolint:errcheck
			return candidate
		}
		if !os.IsExist(err) {
			// 予約できない予期せぬエラー（権限など）。従来挙動に倣い候補をそのまま返し、
			// rename 側でエラーをハンドリングさせる。
			return candidate
		}
	}
}
