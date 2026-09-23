// procs_test.go: procs.go の例示ベーステスト。PBT は pbt_test.go に分離している（PBT-10）。

package main

import (
	"os/exec"
	"testing"
)

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

// 仕様: aidlc-docs/inception/application-design/design.md「yt-dlp の更新（UpdateYtDlp）」
// 停止対象は "downloading" と "paused"。サスペンド中は SIGKILL を受け取れないため
// paused は resume してから Kill する必要がある（needsResume）。
func TestPlanStop(t *testing.T) {
	mk := func(id, status string) *DownloadItem { return &DownloadItem{ID: id, Status: status} }

	t.Run("実行中は resume 不要で停止対象", func(t *testing.T) {
		items := []*DownloadItem{mk("a", "downloading")}
		got := planStop(items)
		if len(got) != 1 {
			t.Fatalf("got %d 件, want 1", len(got))
		}
		if got[0].item != items[0] {
			t.Errorf("別のアイテムが返された")
		}
		if got[0].needsResume {
			t.Errorf("downloading に needsResume=true は不要")
		}
	})

	t.Run("一時停止中は resume してから停止する", func(t *testing.T) {
		items := []*DownloadItem{mk("a", "paused")}
		got := planStop(items)
		if len(got) != 1 {
			t.Fatalf("got %d 件, want 1", len(got))
		}
		if !got[0].needsResume {
			t.Errorf("paused は needsResume=true でなければならない（サスペンド中は SIGKILL を受け取れない）")
		}
	})

	t.Run("プロセスを持たない状態は対象外", func(t *testing.T) {
		items := []*DownloadItem{mk("a", "queued"), mk("b", "finished"), mk("c", "error"), mk("d", "cancelled")}
		if got := planStop(items); len(got) != 0 {
			t.Errorf("got %d 件, want 0", len(got))
		}
	})

	t.Run("順序を保って複数返す", func(t *testing.T) {
		items := []*DownloadItem{mk("a", "queued"), mk("b", "downloading"), mk("c", "paused"), mk("d", "downloading")}
		got := planStop(items)
		if len(got) != 3 {
			t.Fatalf("got %d 件, want 3", len(got))
		}
		for i, want := range []string{"b", "c", "d"} {
			if got[i].item.ID != want {
				t.Errorf("got[%d].ID = %q, want %q", i, got[i].item.ID, want)
			}
		}
	})

	t.Run("状態を変更しない", func(t *testing.T) {
		items := []*DownloadItem{mk("a", "downloading"), mk("b", "paused")}
		planStop(items)
		if items[0].Status != "downloading" || items[1].Status != "paused" {
			t.Errorf("状態が変更された: %q, %q", items[0].Status, items[1].Status)
		}
	})
}

// 仕様: design.md「停止対象は『実行中』だけでは足りない」
// yt-dlp を起こすすべての経路が登録簿を通る。更新中は新規起動を拒否する。
func TestProcRegistry(t *testing.T) {
	t.Run("登録と解除で生存数が増減する", func(t *testing.T) {
		a := NewApp()
		c1 := exec.Command("true")
		c2 := exec.Command("true")

		if got := a.liveProcCount(); got != 0 {
			t.Fatalf("初期値 = %d, want 0", got)
		}
		if !a.registerProc(c1) {
			t.Fatal("registerProc が false を返した")
		}
		if !a.registerProc(c2) {
			t.Fatal("registerProc が false を返した")
		}
		if got := a.liveProcCount(); got != 2 {
			t.Errorf("登録後 = %d, want 2", got)
		}
		a.unregisterProc(c1)
		if got := a.liveProcCount(); got != 1 {
			t.Errorf("解除後 = %d, want 1", got)
		}
		a.unregisterProc(c2)
		if got := a.liveProcCount(); got != 0 {
			t.Errorf("全解除後 = %d, want 0", got)
		}
	})

	t.Run("未登録の解除は何もしない", func(t *testing.T) {
		a := NewApp()
		a.unregisterProc(exec.Command("true"))
		if got := a.liveProcCount(); got != 0 {
			t.Errorf("got %d, want 0", got)
		}
	})

	t.Run("更新中は新規登録を拒否する", func(t *testing.T) {
		a := NewApp()
		a.beginUpdate()
		if a.registerProc(exec.Command("true")) {
			t.Error("更新中に registerProc が true を返した（新規 yt-dlp 起動を許してしまう）")
		}
		if got := a.liveProcCount(); got != 0 {
			t.Errorf("拒否したのに登録された: %d 件", got)
		}
		a.endUpdate()
		if !a.registerProc(exec.Command("true")) {
			t.Error("endUpdate 後に registerProc が false を返した")
		}
	})
}
