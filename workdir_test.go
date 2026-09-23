// workdir_test.go: workdir.go の例示ベーステスト。PBT は pbt_test.go に分離している（PBT-10）。

package main

import "testing"

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
