// app_test.go: app.go（App 本体・scheduler）の例示ベーステスト。PBT は pbt_test.go に分離している（PBT-10）。

package main

import "testing"

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

// 仕様: aidlc-docs/inception/application-design/design.md「自動補充ルール（scheduler）」
// active < maxActive の間だけ先頭から queued を選ぶ。状態は変更しない。
func TestSelectToStart(t *testing.T) {
	mk := func(status string) *DownloadItem { return &DownloadItem{Status: status} }

	t.Run("空きありで先頭から補充", func(t *testing.T) {
		items := []*DownloadItem{mk("queued"), mk("queued"), mk("queued")}
		got := selectToStart(items, 2)
		if len(got) != 2 || got[0] != items[0] || got[1] != items[1] {
			t.Errorf("got %d 件, want 先頭2件", len(got))
		}
	})

	t.Run("実行中が上限なら何も起動しない", func(t *testing.T) {
		items := []*DownloadItem{mk("downloading"), mk("queued")}
		if got := selectToStart(items, 1); len(got) != 0 {
			t.Errorf("got %d 件, want 0", len(got))
		}
	})

	t.Run("実行中を差し引いた残り分だけ補充", func(t *testing.T) {
		items := []*DownloadItem{mk("downloading"), mk("queued"), mk("queued")}
		if got := selectToStart(items, 3); len(got) != 2 {
			t.Errorf("got %d 件, want 2", len(got))
		}
	})

	t.Run("queued が無ければ空", func(t *testing.T) {
		items := []*DownloadItem{mk("downloading"), mk("paused"), mk("finished")}
		if got := selectToStart(items, 5); len(got) != 0 {
			t.Errorf("got %d 件, want 0", len(got))
		}
	})

	t.Run("状態を変更しない", func(t *testing.T) {
		items := []*DownloadItem{mk("queued")}
		selectToStart(items, 1)
		if items[0].Status != "queued" {
			t.Errorf("状態が変更された: %q", items[0].Status)
		}
	})

	// 仕様: design.md「maxActive == 0（登録のみモード）」
	// 0 のときは自動補充を一切行わない。0 専用の分岐を足さず、
	// active < maxActive の一般ルールだけでこれが成り立つことを固定する。
	t.Run("maxActive が 0 なら何も起動しない", func(t *testing.T) {
		items := []*DownloadItem{mk("queued"), mk("queued")}
		if got := selectToStart(items, 0); len(got) != 0 {
			t.Errorf("got %d 件, want 0（登録のみモード）", len(got))
		}
	})

	t.Run("maxActive が 0 でも実行中は無視される（停止させない責務は呼び出し側にない）", func(t *testing.T) {
		items := []*DownloadItem{mk("downloading"), mk("queued")}
		if got := selectToStart(items, 0); len(got) != 0 {
			t.Errorf("got %d 件, want 0", len(got))
		}
		if items[0].Status != "downloading" {
			t.Errorf("実行中アイテムの状態が変更された: %q", items[0].Status)
		}
	})
}

// 仕様: aidlc-docs/inception/requirements/requirements.md「同時ダウンロード数 0（登録のみモード）」
// 0 へ引き下げても実行中・一時停止中のアイテムの状態を変えない（デグレ防止の回帰テスト）。
func TestSetMaxConcurrentZeroDoesNotTouchItems(t *testing.T) {
	a := NewApp()
	a.SetMaxConcurrent(3)
	a.items = []*DownloadItem{
		{ID: "1", Status: "downloading"},
		{ID: "2", Status: "downloading"},
		{ID: "3", Status: "queued"},
		{ID: "4", Status: "paused"},
	}
	before := make([]string, len(a.items))
	for i, it := range a.items {
		before[i] = it.Status
	}

	a.SetMaxConcurrent(0)

	if got := a.GetMaxConcurrent(); got != 0 {
		t.Fatalf("GetMaxConcurrent() = %d, want 0", got)
	}
	for i, it := range a.items {
		if it.Status != before[i] {
			t.Errorf("items[%d] (%s) の状態が %q から %q に変わった。0 への引き下げで実行中を止めてはいけない",
				i, it.ID, before[i], it.Status)
		}
	}
}
