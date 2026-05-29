package main

import "testing"

// 仕様: docs/design.md「プレイリスト・ファイル選択」「Go 側 API」
// 各行 1 JSON。webpage_url 優先・無ければ url。URL 無し行とパース不能行はスキップ。
// 有効エントリ 0 件ならエラー。
func TestParsePlaylistJSON(t *testing.T) {
	t.Run("単一動画", func(t *testing.T) {
		out := []byte(`{"id":"abc","webpage_url":"https://x/v/abc","title":"T","duration":65}`)
		entries, err := parsePlaylistJSON(out)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 {
			t.Fatalf("len = %d, want 1", len(entries))
		}
		e := entries[0]
		if e.ID != "abc" || e.URL != "https://x/v/abc" || e.Title != "T" || e.Duration != "1:05" {
			t.Errorf("entry = %+v", e)
		}
	})

	t.Run("複数行と webpage_url フォールバック", func(t *testing.T) {
		out := []byte(
			`{"id":"1","webpage_url":"https://x/1","title":"A"}` + "\n" +
				`{"id":"2","url":"https://x/2","title":"B"}` + "\n", // url のみ
		)
		entries, err := parsePlaylistJSON(out)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 2 {
			t.Fatalf("len = %d, want 2", len(entries))
		}
		if entries[1].URL != "https://x/2" {
			t.Errorf("url フォールバック失敗: %q", entries[1].URL)
		}
	})

	t.Run("空行・壊れた行・URL無し行はスキップ", func(t *testing.T) {
		out := []byte(
			"\n" +
				`not-json` + "\n" +
				`{"id":"3","title":"NoURL"}` + "\n" + // URL 無し
				`{"id":"4","webpage_url":"https://x/4"}` + "\n",
		)
		entries, err := parsePlaylistJSON(out)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].ID != "4" {
			t.Fatalf("entries = %+v", entries)
		}
	})

	t.Run("有効エントリ0件はエラー", func(t *testing.T) {
		if _, err := parsePlaylistJSON([]byte("not-json\n\n")); err == nil {
			t.Error("エラーになるべき")
		}
	})
}

// 仕様: docs/design.md「(1) サフィックス問題」
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

// 仕様: docs/design.md「自動補充ルール（scheduler）」
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
}

// 仕様: docs/design.md「インストール時の完全性検証」
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
