// ytdlp_test.go: ytdlp.go の例示ベーステスト。PBT は pbt_test.go に分離している（PBT-10）。

package main

import (
	"os/exec"
	"testing"
)

// 仕様: design.md「yt-dlp の更新（UpdateYtDlp）」
// 確認ダイアログ用の影響件数。Items は 0% からやり直しになるアイテム数、
// OtherProcs は情報取得中などアイテムに紐づかない生存プロセス数。
func TestComputeUpdateImpact(t *testing.T) {
	mk := func(status string) *DownloadItem { return &DownloadItem{Status: status} }

	t.Run("影響なし", func(t *testing.T) {
		got := computeUpdateImpact(nil, 0)
		if got.Items != 0 || got.OtherProcs != 0 {
			t.Errorf("got %+v, want ゼロ値", got)
		}
		if got.Affected() {
			t.Errorf("影響なしなら Affected() は false（確認ダイアログを出さない）")
		}
	})

	t.Run("実行中と一時停止中を数える", func(t *testing.T) {
		items := []*DownloadItem{mk("downloading"), mk("paused"), mk("queued")}
		got := computeUpdateImpact(items, 2)
		if got.Items != 2 {
			t.Errorf("Items = %d, want 2", got.Items)
		}
		if got.OtherProcs != 0 {
			t.Errorf("OtherProcs = %d, want 0", got.OtherProcs)
		}
		if !got.Affected() {
			t.Errorf("影響ありなら Affected() は true")
		}
	})

	t.Run("アイテムに紐づかない生存プロセスは OtherProcs", func(t *testing.T) {
		// 取得中（FetchPlaylist）が 2 件走っている状態
		got := computeUpdateImpact([]*DownloadItem{mk("queued")}, 2)
		if got.Items != 0 || got.OtherProcs != 2 {
			t.Errorf("got %+v, want Items=0 OtherProcs=2", got)
		}
		if !got.Affected() {
			t.Errorf("取得中だけでも停止は発生するので Affected() は true")
		}
	})

	t.Run("生存プロセス数がアイテム数を下回っても負にしない", func(t *testing.T) {
		// STEP2a 中のアイテムは item.cmd が未設定で登録簿にも載らないことがある
		items := []*DownloadItem{mk("downloading"), mk("downloading")}
		if got := computeUpdateImpact(items, 0); got.OtherProcs != 0 {
			t.Errorf("OtherProcs = %d, want 0（負にしない）", got.OtherProcs)
		}
	})
}

// 仕様: design.md「yt-dlp の更新（UpdateYtDlp）」バージョン表示
func TestParseYtDlpVersion(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"2026.08.01\n", "2026.08.01"},
		{"  2026.08.01  \n", "2026.08.01"},
		{"2026.08.01\nextra line\n", "2026.08.01"}, // 1 行目のみ採用
		{"", ""},
		{"\n\n", ""},
	}
	for _, c := range cases {
		if got := parseYtDlpVersion([]byte(c.in)); got != c.want {
			t.Errorf("parseYtDlpVersion(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// 仕様: requirements.md「yt-dlp の更新」
// 停止されたダウンロードは待ちキューへ戻り、進捗は 0% にリセットされる
// （workDir が破棄されるため再開ではなく再実行になる）。
func TestResetForRequeue(t *testing.T) {
	item := &DownloadItem{
		ID: "1", Status: "downloading",
		Percent: 42.5, Speed: "1.2MiB/s", ETA: "00:30", Elapsed: "1:05", TotalSize: "100MiB",
		Error: "前回のエラー",
		cmd:   exec.Command("true"),
	}
	item.markStoppedForUpdate()

	resetForRequeue(item)

	if item.Status != "queued" {
		t.Errorf("Status = %q, want \"queued\"", item.Status)
	}
	if item.Percent != 0 || item.Speed != "" || item.ETA != "" || item.Elapsed != "" || item.TotalSize != "" {
		t.Errorf("進捗表示がリセットされていない: %+v", item)
	}
	if item.Error != "" {
		t.Errorf("Error = %q, want 空（更新による停止はエラーではない）", item.Error)
	}
	if item.cmd != nil {
		t.Errorf("cmd がクリアされていない")
	}
	if item.isStoppedForUpdate() {
		t.Errorf("停止フラグがクリアされていない（次回の Wait で再キュー扱いになってしまう）")
	}
}
