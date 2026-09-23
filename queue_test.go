// queue_test.go: queue.go の例示ベーステスト。PBT は pbt_test.go に分離している（PBT-10）。

package main

import (
	"strings"
	"testing"
)

// 仕様: aidlc-docs/inception/application-design/design.md「拒否した理由を返す（沈黙させない）」
// 登録を拒否したら理由を返す。黙って消えてはならない。
func TestAddRejection(t *testing.T) {
	items := []*DownloadItem{
		{ID: "1", URL: "https://e.com/a", Status: "queued"},
		{ID: "2", URL: "https://e.com/b", Status: "error"},
	}

	cases := []struct {
		name string
		url  string
		want string
	}{
		{"受理される新規 URL", "https://e.com/c", ""},
		{"重複（待機中）", "https://e.com/a", "duplicate"},
		{"重複（エラー状態も対象）", "https://e.com/b", "duplicate"},
		{"不正な URL（スキーム違反）", "file:///etc/passwd", "invalid"},
		{"不正な URL（引数インジェクション）", "--exec=touch /tmp/x", "invalid"},
		{"空文字", "", "invalid"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := addRejection(items, c.url); got != c.want {
				t.Errorf("addRejection(%q) = %q, want %q", c.url, got, c.want)
			}
		})
	}

	// 不正 URL の判定が重複判定より先であること（不正な値を重複扱いにしない）。
	t.Run("不正判定が重複判定より先", func(t *testing.T) {
		withBad := []*DownloadItem{{ID: "9", URL: "file:///x", Status: "queued"}}
		if got := addRejection(withBad, "file:///x"); got != "invalid" {
			t.Errorf("addRejection = %q, want \"invalid\"", got)
		}
	})
}

func TestAddRejectionMessage(t *testing.T) {
	// 受理時は文言を出さない。
	if got := addRejectionMessage(""); got != "" {
		t.Errorf("受理時の文言 = %q, want \"\"", got)
	}
	// 拒否理由には、ユーザーが次に何をすればよいか分かる情報を含める。
	for _, c := range []struct {
		reason string
		want   []string
	}{
		{"invalid", []string{"http"}},
		{"duplicate", []string{"登録"}},
	} {
		got := addRejectionMessage(c.reason)
		if got == "" {
			t.Errorf("reason=%q の文言が空（黙って消えてしまう）", c.reason)
			continue
		}
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("reason=%q の文言 %q に %q が含まれない", c.reason, got, w)
			}
		}
	}
	// 未知の理由でも空にしない（沈黙させない）。
	if got := addRejectionMessage("something-new"); got == "" {
		t.Error("未知の理由で文言が空になった（沈黙させてはいけない）")
	}
}

// AddToQueue は拒否理由を構造化して返す。
func TestAddToQueueReportsRejection(t *testing.T) {
	t.Run("受理すると ID を返し理由は空", func(t *testing.T) {
		a := NewApp()
		got := a.AddToQueue("https://e.com/v", "/tmp")
		if got.Reason != "" || got.Message != "" {
			t.Errorf("受理なのに拒否扱い: %+v", got)
		}
		if got.ID == "" {
			t.Error("受理なのに ID が空")
		}
		if len(a.items) != 1 {
			t.Errorf("items 件数 = %d, want 1", len(a.items))
		}
	})

	t.Run("重複は理由と文言を返し、リストは増えない", func(t *testing.T) {
		a := NewApp()
		a.AddToQueue("https://e.com/v", "/tmp")
		got := a.AddToQueue("https://e.com/v", "/tmp")
		if got.Reason != "duplicate" {
			t.Errorf("Reason = %q, want \"duplicate\"", got.Reason)
		}
		if got.Message == "" {
			t.Error("Message が空（ユーザーに理由が伝わらない）")
		}
		if got.ID != "" {
			t.Errorf("拒否なのに ID が返った: %q", got.ID)
		}
		if len(a.items) != 1 {
			t.Errorf("重複でリストが増えた: %d 件", len(a.items))
		}
	})

	t.Run("不正な URL は理由と文言を返し、登録しない", func(t *testing.T) {
		a := NewApp()
		got := a.AddToQueue("--exec=touch /tmp/x", "/tmp")
		if got.Reason != "invalid" {
			t.Errorf("Reason = %q, want \"invalid\"", got.Reason)
		}
		if got.Message == "" {
			t.Error("Message が空")
		}
		if len(a.items) != 0 {
			t.Errorf("不正 URL を登録してしまった: %d 件", len(a.items))
		}
	})
}

// 仕様: aidlc-docs/inception/application-design/design.md「Referer のユーザー指定（元ページ URL）」
func TestRetryWithReferer(t *testing.T) {
	useTempLog(t)
	const page = "https://www.example.com/video/123/"

	newErrApp := func() (*App, *DownloadItem) {
		a := NewApp()
		item := &DownloadItem{
			ID: "1", URL: "https://cdn.other.net/hls/x/master.m3u8",
			Status: "error", Error: "アクセスが拒否されました（403）。",
			Percent: 42, Speed: "1.5MiB/s",
		}
		a.items = append(a.items, item)
		return a, item
	}

	t.Run("元ページ URL を設定して待ちキューへ戻す", func(t *testing.T) {
		a, item := newErrApp()
		if msg := a.RetryWithReferer("1", page); msg != "" {
			t.Fatalf("エラー文言が返った: %q", msg)
		}
		if item.Referer != page {
			t.Errorf("Referer = %q, want %q", item.Referer, page)
		}
		if item.Status != "queued" {
			t.Errorf("Status = %q, want \"queued\"", item.Status)
		}
		if item.Error != "" || item.Percent != 0 || item.Speed != "" {
			t.Errorf("再試行の前提がクリアされていない: %+v", item)
		}
	})

	t.Run("設定した Referer は実際に yt-dlp の引数へ渡る", func(t *testing.T) {
		a, item := newErrApp()
		a.RetryWithReferer("1", page)
		s := strings.Join(buildYtDlpArgs("abc", "/work", "", item.URL, item.Referer), " ")
		if !strings.Contains(s, "--referer "+page) {
			t.Errorf("引数に渡っていない: %q", s)
		}
	})

	t.Run("不正な元ページ URL は受け付けずアイテムを変えない", func(t *testing.T) {
		for _, bad := range []string{"--referer=http://evil/", "-J", "not-a-url", "file:///etc/passwd", "", "   "} {
			a, item := newErrApp()
			msg := a.RetryWithReferer("1", bad)
			if msg == "" {
				t.Errorf("pageURL=%q を受理してしまった", bad)
			}
			if item.Referer != "" {
				t.Errorf("pageURL=%q で Referer が設定された: %q", bad, item.Referer)
			}
			if item.Status != "error" {
				t.Errorf("pageURL=%q でアイテムの状態が変わった: %q", bad, item.Status)
			}
		}
	})

	t.Run("存在しない ID・エラー以外の状態は受け付けない", func(t *testing.T) {
		a, item := newErrApp()
		if msg := a.RetryWithReferer("nope", page); msg == "" {
			t.Error("存在しない ID を受理してしまった")
		}
		item.Status = "downloading"
		if msg := a.RetryWithReferer("1", page); msg == "" {
			t.Error("downloading 状態を受理してしまった")
		}
	})

	// Referer は以降のリトライ・再キューで消えてはいけない（消えたら再試行の意味がない）。
	t.Run("Referer はリトライと再キューで保持される", func(t *testing.T) {
		a, item := newErrApp()
		a.RetryWithReferer("1", page)

		item.Status = "error"
		a.RetryDownload("1")
		if item.Referer != page {
			t.Errorf("RetryDownload で Referer が消えた: %q", item.Referer)
		}

		resetForRequeue(item)
		if item.Referer != page {
			t.Errorf("resetForRequeue で Referer が消えた: %q", item.Referer)
		}
	})
}

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
