// checksum_test.go: checksum.go の例示ベーステスト。PBT は pbt_test.go に分離している（PBT-10）。

package main

import "testing"

// 仕様: aidlc-docs/inception/application-design/design.md「インストール時の完全性検証」
// SHA2-256SUMS の各行 "<hexdigest>  <filename>" から assetName 行の値を返す。
func TestParseSums(t *testing.T) {
	data := []byte(
		"aaa111  yt-dlp\n" +
			"bbb222  yt-dlp.exe\n" +
			"ccc333  yt-dlp_macos\n",
	)
	got, err := parseSums(data, "yt-dlp_macos")
	if err != nil {
		t.Fatal(err)
	}
	if got != "ccc333" {
		t.Errorf("got %q, want ccc333", got)
	}

	if _, err := parseSums(data, "nonexistent"); err == nil {
		t.Error("見つからない場合はエラーになるべき")
	}
}
