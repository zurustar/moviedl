package main

import (
	"os/exec"
	"testing"
)

// 仕様: aidlc-docs/inception/application-design/design.md「プレイリスト・ファイル選択」「Go 側 API」
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

// 仕様: aidlc-docs/inception/application-design/design.md「キュー登録（AddToQueue）と重複防止」
// items のいずれかが同一 URL を持てば true（完全一致）。状態は問わない。
func TestContainsURL(t *testing.T) {
	items := []*DownloadItem{
		{URL: "https://x/a", Status: "downloading"},
		{URL: "https://x/b", Status: "queued"},
		{URL: "https://x/c", Status: "error"},
	}
	cases := []struct {
		url  string
		want bool
	}{
		{"https://x/b", true},      // queued と一致
		{"https://x/a", true},      // downloading と一致
		{"https://x/c", true},      // error とも一致（状態は問わない）
		{"https://x/d", false},     // 未登録
		{"https://x/b?z=1", false}, // 完全一致のみ
		{"", false},
	}
	for _, c := range cases {
		if got := containsURL(items, c.url); got != c.want {
			t.Errorf("containsURL(_, %q) = %v, want %v", c.url, got, c.want)
		}
	}
	if containsURL(nil, "https://x/a") {
		t.Error("空リストで true になった")
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

// 仕様: aidlc-docs/inception/application-design/design.md「Windows ffmpeg の取得」
// zip エントリ名一覧から basename が ffmpeg.exe のエントリを返す。
func TestFfmpegZipEntry(t *testing.T) {
	t.Run("bin/ffmpeg.exe を選ぶ", func(t *testing.T) {
		names := []string{
			"ffmpeg-master-latest-win64-gpl/",
			"ffmpeg-master-latest-win64-gpl/bin/",
			"ffmpeg-master-latest-win64-gpl/bin/ffprobe.exe",
			"ffmpeg-master-latest-win64-gpl/bin/ffmpeg.exe",
			"ffmpeg-master-latest-win64-gpl/LICENSE.txt",
		}
		got, err := ffmpegZipEntry(names)
		if err != nil {
			t.Fatal(err)
		}
		if got != "ffmpeg-master-latest-win64-gpl/bin/ffmpeg.exe" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("ffmpeg.exe が無ければエラー", func(t *testing.T) {
		names := []string{"x/bin/ffprobe.exe", "x/bin/notffmpeg.exe"}
		if _, err := ffmpegZipEntry(names); err == nil {
			t.Error("エラーになるべき")
		}
	})

	t.Run("末尾一致の誤検出をしない", func(t *testing.T) {
		// basename が "myffmpeg.exe" は ffmpeg.exe ではない
		names := []string{"x/bin/myffmpeg.exe"}
		if _, err := ffmpegZipEntry(names); err == nil {
			t.Error("myffmpeg.exe を誤って採用した")
		}
	})
}

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

// 仕様: design.md「yt-dlp の更新（UpdateYtDlp）」
// runDownload 終了時にリストから取り除くべき状態を判定する。
// error は「リトライ・削除できるよう残す」、queued は「更新のため停止して
// 待ちキューへ戻したものが再開できるよう残す」。どちらも消してはいけない。
func TestShouldRemoveWhenDone(t *testing.T) {
	cases := []struct {
		status string
		want   bool
	}{
		{"finished", true},
		{"cancelled", true},
		{"error", false},  // リトライ・URL コピーのために残す
		{"queued", false}, // 更新のため停止して再キューしたアイテムを消さない
	}
	for _, c := range cases {
		if got := shouldRemoveWhenDone(c.status); got != c.want {
			t.Errorf("shouldRemoveWhenDone(%q) = %v, want %v", c.status, got, c.want)
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
