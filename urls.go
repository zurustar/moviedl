// urls.go: URL の検証と m3u8 判定・Referer の決定（引数インジェクション対策を含む）。

package main

import (
	neturl "net/url"
	"strings"
)

// isValidURL は yt-dlp に渡してよい URL かを判定する。
// http:// または https:// で始まることを要求し、引数インジェクション
// （URL が "-" 始まりで yt-dlp のオプションに化ける）を入口で防ぐ。
// 詳細は aidlc-docs/inception/application-design/design.md「引数インジェクション対策」を参照。
func isValidURL(raw string) bool {
	u, err := neturl.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// isM3U8URL は URL の**パス**が .m3u8 で終わるかを判定する（大小無視）。
//
// m3u8 向けの分岐（Referer 付与・保存名の生成・登録時の警告）は**すべてこの述語に集約する**。
// 3 箇所が別々の判定を持つと、片方だけ直したときに挙動が食い違う。
//
// クエリ文字列は判定に含めない。m3u8 の URL は "...master.m3u8?token=x" のようにクエリ付きが
// 普通であり、生文字列の strings.HasSuffix では末尾が .m3u8 にならないため判定できない。
// 詳細は aidlc-docs/inception/application-design/design.md「判定は 1 つの述語に集約する」を参照。
func isM3U8URL(raw string) bool {
	if !isValidURL(raw) {
		return false
	}
	u, err := neturl.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return strings.HasSuffix(strings.ToLower(u.Path), ".m3u8")
}

// refererFor は m3u8 URL のオリジン（scheme://host/）を Referer として返す。
// m3u8 でない URL・不正な URL には "" を返し、呼び出し側は --referer を付けない。
//
// 付与を m3u8 に限定するのは**デグレ防止のため**。既存の 1000 以上の対応サイトは現在 Referer
// なしで動作しており、全 URL に送り始めるとそれらの経路の挙動を変えてしまう。
//
// 組み立てた値は isValidURL で再検証してから返す。yt-dlp のオプションに化ける値
// （"-" 始まり）を渡さないための多層防御で、design.md「引数インジェクション対策（-- 終端は必須）」
// と同じ思想。詳細は design.md「Referer は m3u8 URL に限って付与する」を参照。
func refererFor(raw string) string {
	if !isM3U8URL(raw) {
		return ""
	}
	u, err := neturl.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	// パス・クエリ・フラグメントは落とし、オリジンだけを渡す。
	origin := u.Scheme + "://" + u.Host + "/"
	if !isValidURL(origin) {
		return ""
	}
	return origin
}

// effectiveReferer は yt-dlp へ渡す Referer を決める。
//
// ユーザーが明示した元ページ URL（override）があればそれを優先し、なければ
// m3u8 の自動導出（refererFor）に落ちる。
//
// **なぜユーザー指定が必要か:** 自動導出は m3u8 自身のオリジンを返すため、
// プレイヤーや CDN が視聴ページと別ドメインにある構成では効かない（よくある構成）。
//
// **なぜユーザー指定は m3u8 に限定しないか:** 自動付与を m3u8 に限ったのは既存の
// 対応サイトの挙動を変えないため。ユーザーが明示したものはその限定の対象外。
//
// 不正な override は無視して自動導出に落ちる。`-` 始まりの値が `--referer` の
// 引数に化けるのを防ぐ多層防御（design.md「引数インジェクション対策」と同じ思想）。
// 入口の検証は RetryWithReferer が行い、ここは最後の砦。
func effectiveReferer(url, override string) string {
	if o := strings.TrimSpace(override); o != "" && isValidURL(o) {
		return o
	}
	return refererFor(url)
}
