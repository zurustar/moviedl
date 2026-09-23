// pbt_test.go はプロパティベーステスト（PBT）専用のファイル。
// 例示ベーステスト（app_test.go / helpers_test.go）を置き換えるものではなく、
// 一般ルールが任意の入力で成り立つことを検証して併存させる（PBT-10）。
//
// 仕様の出典:
//   - aidlc-docs/inception/application-design/design.md「テスト可能プロパティ（PBT-01）」
//   - aidlc-docs/inception/requirements/requirements.md「同時ダウンロード数 0（登録のみモード）」
//
// 失敗時は rapid が最小反例と `-rapid.seed=N` を出力する（PBT-08）。
// 再現: go test -run TestProp... -rapid.seed=N
// CI は RAPID_SEED を記録して同じシードで再実行できるようにする（.github/workflows/ci.yml）。
package main

import (
	"math"
	"os/exec"
	"strings"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// maxConcurrentBounds は SetMaxConcurrent が受理する設定値の範囲。
const (
	maxConcurrentMin = 0
	maxConcurrentMax = 10
)

// itemStatuses は DownloadItem.Status が取り得る値の全体集合。
// design.md「単一リストモデル」の表と一致させる。
var itemStatuses = []string{"queued", "downloading", "paused", "finished", "error", "cancelled"}

// genStatus は実在する Status 値だけを生成する（PBT-07: 生の文字列を Status に入れない）。
func genStatus() *rapid.Generator[string] {
	return rapid.SampledFrom(itemStatuses)
}

// genItems は DownloadItem スライスのドメインジェネレータ。
// selectToStart が参照するのは Status と並び順だけなので、ID は識別用に採番する。
func genItems() *rapid.Generator[[]*DownloadItem] {
	return rapid.Custom(func(t *rapid.T) []*DownloadItem {
		statuses := rapid.SliceOfN(genStatus(), 0, 20).Draw(t, "statuses")
		items := make([]*DownloadItem, len(statuses))
		for i, s := range statuses {
			items[i] = &DownloadItem{ID: rapid.StringMatching(`[a-z]{3}`).Draw(t, "id"), Status: s}
		}
		return items
	})
}

// genAnyConcurrency は境界値付近を厚めに含む「任意の設定値」ジェネレータ。
// フロントエンド以外の経路（将来の API 呼び出しなど）から範囲外が来ても壊れないことを検証する。
func genAnyConcurrency() *rapid.Generator[int] {
	return rapid.OneOf(
		rapid.IntRange(maxConcurrentMin, maxConcurrentMax), // 正常系
		rapid.IntRange(-3, 13),                             // 境界のすぐ外側
		rapid.Int(),                                        // 極端な値
	)
}

func countStatus(items []*DownloadItem, status string) int {
	n := 0
	for _, it := range items {
		if it.Status == status {
			n++
		}
	}
	return n
}

// プロパティ（範囲制約）: どんな int を渡しても maxActive は 0〜10 に収まる。
func TestPropSetMaxConcurrentStaysInRange(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := genAnyConcurrency().Draw(t, "n")

		a := NewApp()
		a.SetMaxConcurrent(n)

		got := a.GetMaxConcurrent()
		if got < maxConcurrentMin || got > maxConcurrentMax {
			t.Fatalf("SetMaxConcurrent(%d) 後の値 %d が [%d,%d] の外",
				n, got, maxConcurrentMin, maxConcurrentMax)
		}
	})
}

// プロパティ（恒等）: 範囲内の値はクランプされずそのまま保持される。
// 0 も範囲内なので、このプロパティが 0 の保持を保証する。
func TestPropSetMaxConcurrentIdentityInRange(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(maxConcurrentMin, maxConcurrentMax).Draw(t, "n")

		a := NewApp()
		a.SetMaxConcurrent(n)

		if got := a.GetMaxConcurrent(); got != n {
			t.Fatalf("SetMaxConcurrent(%d) 後の値 = %d, want %d（範囲内はクランプしない）", n, got, n)
		}
	})
}

// プロパティ（冪等）: 同じ値を 2 回設定しても結果は 1 回と同じ。
func TestPropSetMaxConcurrentIdempotent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := genAnyConcurrency().Draw(t, "n")

		once := NewApp()
		once.SetMaxConcurrent(n)

		twice := NewApp()
		twice.SetMaxConcurrent(n)
		twice.SetMaxConcurrent(n)

		if once.GetMaxConcurrent() != twice.GetMaxConcurrent() {
			t.Fatalf("Set(%d) を 1 回 = %d, 2 回 = %d（冪等でない）",
				n, once.GetMaxConcurrent(), twice.GetMaxConcurrent())
		}
	})
}

// プロパティ（範囲制約 + 登録のみモード）: 起動後の実行中件数は上限を超えず、
// maxActive が 0 なら常に何も起動しない。
func TestPropSelectToStartNeverExceedsLimit(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		items := genItems().Draw(t, "items")
		maxActive := rapid.IntRange(maxConcurrentMin, maxConcurrentMax).Draw(t, "maxActive")

		active := countStatus(items, "downloading")
		got := selectToStart(items, maxActive)

		if maxActive == 0 && len(got) != 0 {
			t.Fatalf("maxActive=0 で %d 件起動しようとした（登録のみモード違反）", len(got))
		}
		// 既に上限を超えて実行中（手動開始は上限の対象外）の場合は、その件数が新たな天井になる。
		ceiling := maxActive
		if active > ceiling {
			ceiling = active
		}
		if active+len(got) > ceiling {
			t.Fatalf("実行中 %d + 起動 %d = %d が上限 %d を超えた", active, len(got), active+len(got), ceiling)
		}
	})
}

// プロパティ（Oracle）: 起動件数は min(queued 件数, max(0, maxActive - 実行中件数)) に一致する。
func TestPropSelectToStartCountMatchesOracle(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		items := genItems().Draw(t, "items")
		maxActive := rapid.IntRange(maxConcurrentMin, maxConcurrentMax).Draw(t, "maxActive")

		active := countStatus(items, "downloading")
		queued := countStatus(items, "queued")

		slots := maxActive - active
		if slots < 0 {
			slots = 0
		}
		want := slots
		if queued < want {
			want = queued
		}

		if got := len(selectToStart(items, maxActive)); got != want {
			t.Fatalf("selectToStart(実行中=%d, queued=%d, maxActive=%d) = %d 件, want %d 件",
				active, queued, maxActive, got, want)
		}
	})
}

// プロパティ（要素保存 + 順序保存）: 返り値は入力に含まれる queued アイテムのみで、入力順を保つ。
func TestPropSelectToStartReturnsQueuedItemsInOrder(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		items := genItems().Draw(t, "items")
		maxActive := rapid.IntRange(maxConcurrentMin, maxConcurrentMax).Draw(t, "maxActive")

		got := selectToStart(items, maxActive)

		index := make(map[*DownloadItem]int, len(items))
		for i, it := range items {
			index[it] = i
		}

		prev := -1
		for _, it := range got {
			pos, ok := index[it]
			if !ok {
				t.Fatalf("入力に存在しないアイテムが返された: %+v", it)
			}
			if it.Status != "queued" {
				t.Fatalf("queued 以外のアイテムが返された: status=%q", it.Status)
			}
			if pos <= prev {
				t.Fatalf("入力順が保たれていない: 位置 %d が前の位置 %d 以下", pos, prev)
			}
			prev = pos
		}
	})
}

// プロパティ（副作用なし）: selectToStart は状態を変更しない（変更は呼び出し側の責務）。
func TestPropSelectToStartHasNoSideEffects(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		items := genItems().Draw(t, "items")
		maxActive := rapid.IntRange(maxConcurrentMin, maxConcurrentMax).Draw(t, "maxActive")

		before := make([]string, len(items))
		for i, it := range items {
			before[i] = it.Status
		}

		selectToStart(items, maxActive)

		for i, it := range items {
			if it.Status != before[i] {
				t.Fatalf("items[%d] の状態が %q から %q に変更された", i, before[i], it.Status)
			}
		}
	})
}

// --- yt-dlp 更新（UpdateYtDlp）のプロパティ ---
// 仕様: design.md「yt-dlp の更新（UpdateYtDlp）」テスト可能プロパティ（PBT-01）

// プロパティ（要素保存 + 順序保存）: planStop はプロセスを持つ状態のアイテムのみを
// 入力順で返す。
func TestPropPlanStopSelectsOnlyLiveStatusesInOrder(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		items := genItems().Draw(t, "items")

		got := planStop(items)

		index := make(map[*DownloadItem]int, len(items))
		for i, it := range items {
			index[it] = i
		}
		prev := -1
		for _, target := range got {
			pos, ok := index[target.item]
			if !ok {
				t.Fatalf("入力に存在しないアイテムが返された")
			}
			if target.item.Status != "downloading" && target.item.Status != "paused" {
				t.Fatalf("プロセスを持たない状態が停止対象になった: %q", target.item.Status)
			}
			if pos <= prev {
				t.Fatalf("入力順が保たれていない: 位置 %d が前の位置 %d 以下", pos, prev)
			}
			prev = pos
		}
	})
}

// プロパティ（Oracle）: 停止対象数は downloading + paused の件数に一致する。
func TestPropPlanStopCountMatchesOracle(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		items := genItems().Draw(t, "items")

		want := countStatus(items, "downloading") + countStatus(items, "paused")
		if got := len(planStop(items)); got != want {
			t.Fatalf("planStop = %d 件, want %d 件", got, want)
		}
	})
}

// プロパティ（不変条件）: needsResume は paused のときだけ真。
// サスペンド中のプロセスは resume しないと SIGKILL を受け取れないため、
// この対応が崩れると停止が効かなくなる。
func TestPropPlanStopNeedsResumeOnlyForPaused(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		items := genItems().Draw(t, "items")

		for _, target := range planStop(items) {
			wantResume := target.item.Status == "paused"
			if target.needsResume != wantResume {
				t.Fatalf("status=%q で needsResume=%v, want %v",
					target.item.Status, target.needsResume, wantResume)
			}
		}
	})
}

// プロパティ（副作用なし）: planStop は状態を変更しない。
func TestPropPlanStopHasNoSideEffects(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		items := genItems().Draw(t, "items")

		before := make([]string, len(items))
		for i, it := range items {
			before[i] = it.Status
		}

		planStop(items)

		for i, it := range items {
			if it.Status != before[i] {
				t.Fatalf("items[%d] の状態が %q から %q に変更された", i, before[i], it.Status)
			}
		}
	})
}

// プロパティ（範囲制約）: OtherProcs は負にならず、Items + OtherProcs は
// アイテム数と生存プロセス数のどちらも超えない形で整合する。
func TestPropComputeUpdateImpactNeverNegative(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		items := genItems().Draw(t, "items")
		liveProcs := rapid.IntRange(0, 40).Draw(t, "liveProcs")

		got := computeUpdateImpact(items, liveProcs)

		if got.Items < 0 || got.OtherProcs < 0 {
			t.Fatalf("負の件数: %+v", got)
		}
		if got.Items != len(planStop(items)) {
			t.Fatalf("Items = %d, want %d（planStop と一致すべき）", got.Items, len(planStop(items)))
		}
		if got.OtherProcs > liveProcs {
			t.Fatalf("OtherProcs = %d が生存プロセス数 %d を超えた", got.OtherProcs, liveProcs)
		}
	})
}

// プロパティ（不変条件）: 影響が無い（停止対象 0 かつ生存プロセス 0）ときだけ
// Affected() が false になる = 確認ダイアログを省略できる条件と一致する。
func TestPropUpdateImpactAffectedMatchesEmptiness(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		items := genItems().Draw(t, "items")
		liveProcs := rapid.IntRange(0, 40).Draw(t, "liveProcs")

		got := computeUpdateImpact(items, liveProcs)

		wantAffected := got.Items > 0 || got.OtherProcs > 0
		if got.Affected() != wantAffected {
			t.Fatalf("Affected() = %v, want %v (%+v)", got.Affected(), wantAffected, got)
		}
	})
}

// プロパティ（冪等）: resetForRequeue を 2 回適用しても 1 回と同じ状態になる。
func TestPropResetForRequeueIdempotent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		mk := func() *DownloadItem {
			return &DownloadItem{
				Status:    genStatus().Draw(t, "status"),
				Percent:   rapid.Float64Range(0, 100).Draw(t, "percent"),
				Speed:     rapid.String().Draw(t, "speed"),
				ETA:       rapid.String().Draw(t, "eta"),
				Elapsed:   rapid.String().Draw(t, "elapsed"),
				TotalSize: rapid.String().Draw(t, "totalSize"),
				Error:     rapid.String().Draw(t, "err"),
			}
		}
		once, twice := mk(), mk()
		*twice = *once

		resetForRequeue(once)
		resetForRequeue(twice)
		resetForRequeue(twice)

		if *once != *twice {
			t.Fatalf("1 回 = %+v, 2 回 = %+v（冪等でない）", *once, *twice)
		}
	})
}

// プロパティ（不変条件）: resetForRequeue 後は必ず "queued" で進捗ゼロ、
// エラーと停止フラグがクリアされている。
func TestPropResetForRequeueAlwaysQueuedAndClean(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		item := &DownloadItem{
			Status:    genStatus().Draw(t, "status"),
			Percent:   rapid.Float64Range(0, 100).Draw(t, "percent"),
			Speed:     rapid.String().Draw(t, "speed"),
			ETA:       rapid.String().Draw(t, "eta"),
			Elapsed:   rapid.String().Draw(t, "elapsed"),
			TotalSize: rapid.String().Draw(t, "totalSize"),
			Error:     rapid.String().Draw(t, "err"),
		}
		if rapid.Bool().Draw(t, "stopped") {
			item.markStoppedForUpdate()
		}

		resetForRequeue(item)

		if item.Status != "queued" {
			t.Fatalf("Status = %q, want queued", item.Status)
		}
		if item.Percent != 0 || item.Speed != "" || item.ETA != "" || item.Elapsed != "" || item.TotalSize != "" {
			t.Fatalf("進捗表示が残った: %+v", *item)
		}
		if item.Error != "" {
			t.Fatalf("Error が残った: %q", item.Error)
		}
		if item.isStoppedForUpdate() {
			t.Fatalf("停止フラグが残った")
		}
		// 再キューしたアイテムはリストに残らなければならない
		if shouldRemoveWhenDone(item.Status) {
			t.Fatalf("再キューしたアイテムがリストから取り除かれる判定になった")
		}
	})
}

// プロパティ（不変条件）: 登録簿は登録・解除の順序に関わらず生存数が一致し、
// 更新中は 1 件も登録されない。
func TestPropProcRegistryCountsCorrectly(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 20).Draw(t, "n")
		updating := rapid.Bool().Draw(t, "updating")

		a := NewApp()
		if updating {
			a.beginUpdate()
		}

		cmds := make([]*exec.Cmd, n)
		accepted := 0
		for i := range cmds {
			cmds[i] = exec.Command("true")
			if a.registerProc(cmds[i]) {
				accepted++
			}
		}

		want := n
		if updating {
			want = 0
		}
		if accepted != want {
			t.Fatalf("登録成功 %d 件, want %d 件", accepted, want)
		}
		if got := a.liveProcCount(); got != want {
			t.Fatalf("生存数 %d, want %d", got, want)
		}

		for _, c := range cmds {
			a.unregisterProc(c)
		}
		if got := a.liveProcCount(); got != 0 {
			t.Fatalf("全解除後の生存数 = %d, want 0", got)
		}
	})
}

// --- m3u8（HLS）対応のプロパティ ---
// 仕様: design.md「m3u8 / HLS」テスト可能プロパティ（PBT-01）
//
// 追加した純粋関数はいずれも「任意の URL 文字列」を入力に取る。そのため
// URL の構成要素（スキーム・ホスト・ポート・パス要素・クエリ）から
// **構造的に妥当な URL を組み立てる専用ジェネレータ**を用意する（PBT-07）。
// 生の文字列をそのまま URL として渡すジェネレータは使わない。

// genHost は妥当なホスト名を生成する。
func genHost() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		labels := rapid.SliceOfN(rapid.StringMatching(`[a-z][a-z0-9-]{0,7}`), 1, 3).Draw(t, "labels")
		tld := rapid.SampledFrom([]string{"com", "net", "jp", "example"}).Draw(t, "tld")
		return strings.Join(append(labels, tld), ".")
	})
}

// genAuthority はホスト（+ 任意のポート）を生成する。
func genAuthority() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		host := genHost().Draw(t, "host")
		if rapid.Bool().Draw(t, "hasPort") {
			return host + ":" + rapid.SampledFrom([]string{"80", "443", "8080", "1935"}).Draw(t, "port")
		}
		return host
	})
}

// genPathSegments はパス要素を生成する。汎用語（hls など）も混ぜて、
// 「末尾から汎用語を飛ばす」ロジックが働く入力を作る（PBT-07: 境界ケースの混入）。
func genPathSegments() *rapid.Generator[[]string] {
	seg := rapid.OneOf(
		rapid.StringMatching(`[a-z0-9_-]{1,10}`),
		rapid.SampledFrom([]string{"hls", "stream", "streams", "media", "vod", "playlist", "HLS", "Stream"}),
	)
	return rapid.SliceOfN(seg, 0, 4)
}

// genQueryToken は有効期限トークン風の値を生成する。パス要素の文字集合（小文字・数字）と
// 衝突しないよう**大文字のみ**にして、「クエリ由来の文字が結果に混ざっていない」ことを
// 誤検知なく検証できるようにする。
func genQueryToken() *rapid.Generator[string] {
	return rapid.StringMatching(`[A-Z]{8,16}`)
}

// genM3U8URL はパスが .m3u8 で終わる妥当な URL を組み立てる。
// 第 2 戻り値はクエリを除いた同じ URL（クエリ非依存性の検証に使う）。
func drawM3U8URL(t *rapid.T) (withQuery, withoutQuery, token string) {
	scheme := rapid.SampledFrom([]string{"http", "https"}).Draw(t, "scheme")
	auth := genAuthority().Draw(t, "authority")
	segs := genPathSegments().Draw(t, "segments")
	base := rapid.SampledFrom([]string{"master", "index", "playlist", "chunklist", "media"}).Draw(t, "base")
	ext := rapid.SampledFrom([]string{".m3u8", ".M3U8", ".M3u8"}).Draw(t, "ext")

	path := "/" + strings.Join(append(segs, base+ext), "/")
	withoutQuery = scheme + "://" + auth + path

	token = genQueryToken().Draw(t, "token")
	withQuery = withoutQuery + "?token=" + token + "&exp=1789944255"
	return withQuery, withoutQuery, token
}

// genAdversarialURLish は refererFor に渡される可能性のある敵対的な入力を生成する。
// 引数インジェクション対策（design.md「引数インジェクション対策（-- 終端は必須）」）が
// 任意の入力で成り立つことを確かめるため、妥当な URL と不正な文字列を混ぜる。
func genAdversarialURLish() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		switch rapid.IntRange(0, 3).Draw(t, "kind") {
		case 0:
			w, _, _ := drawM3U8URL(t)
			return w
		case 1:
			// m3u8 ではない妥当な URL
			auth := genAuthority().Draw(t, "authority")
			return "https://" + auth + "/watch?v=" + rapid.StringMatching(`[a-z0-9]{1,8}`).Draw(t, "v")
		case 2:
			// yt-dlp のオプションに化けうる文字列
			return rapid.SampledFrom([]string{
				"--exec=touch /tmp/pwned.m3u8", "-J", "--config-location=/etc/evil.conf",
				"--referer=http://evil/", "-o-", "file:///tmp/a.m3u8", "", "   ",
			}).Draw(t, "hostile")
		default:
			// スキームやホストを欠いた入力
			return rapid.SampledFrom([]string{
				"example.com/a.m3u8", "https://", "https:///a.m3u8", "//e.com/a.m3u8", "a.m3u8",
			}).Draw(t, "malformed")
		}
	})
}

// プロパティ（クエリ非依存）: クエリを足しても isM3U8URL の判定は変わらない。
// 生文字列の HasSuffix で実装すると必ず落ちるプロパティ。
func TestPropIsM3U8URLIgnoresQuery(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		withQuery, withoutQuery, _ := drawM3U8URL(rt)
		if !isM3U8URL(withoutQuery) {
			rt.Fatalf("クエリなしで m3u8 と判定されない: %q", withoutQuery)
		}
		if !isM3U8URL(withQuery) {
			rt.Fatalf("クエリ付きで m3u8 と判定されない: %q", withQuery)
		}
	})
}

// プロパティ（引数インジェクション）: refererFor は任意の入力に対して "-" 始まりを返さない。
func TestPropRefererForNeverStartsWithDash(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		in := genAdversarialURLish().Draw(rt, "input")
		got := refererFor(in)
		if strings.HasPrefix(got, "-") {
			rt.Fatalf("refererFor(%q) = %q: '-' 始まりは yt-dlp のオプションに化ける", in, got)
		}
	})
}

// プロパティ（対応関係）: 非空を返すのは isM3U8URL が真のときだけ。
// 崩れると m3u8 以外にも Referer が付き、既存サイトの経路の挙動を変えてしまう（デグレ）。
func TestPropRefererForOnlyForM3U8(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		in := genAdversarialURLish().Draw(rt, "input")
		if got, want := refererFor(in) != "", isM3U8URL(in); got != want {
			rt.Fatalf("refererFor(%q) の非空判定 = %v, isM3U8URL = %v", in, got, want)
		}
	})
}

// プロパティ（オリジンのみ）: 戻り値はスキームとオーソリティだけで、パス・クエリ・フラグメントを含まない。
func TestPropRefererForIsOriginOnly(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		withQuery, _, token := drawM3U8URL(rt)
		got := refererFor(withQuery)
		if got == "" {
			rt.Fatalf("m3u8 URL なのに空: %q", withQuery)
		}
		if strings.ContainsAny(got, "?#") {
			rt.Fatalf("refererFor(%q) = %q にクエリ/フラグメントが含まれる", withQuery, got)
		}
		if strings.Contains(got, token) {
			rt.Fatalf("refererFor(%q) = %q にトークンが含まれる", withQuery, got)
		}
		// "scheme://authority/" の形（末尾スラッシュより後ろにパスがない）
		if n := strings.Count(strings.TrimPrefix(got, "http://"), "/"); n != 1 {
			if n := strings.Count(strings.TrimPrefix(got, "https://"), "/"); n != 1 {
				rt.Fatalf("refererFor(%q) = %q はオリジンの形ではない", withQuery, got)
			}
		}
	})
}

// プロパティ（安全なファイル名）: 生成名はそのままファイル名として使える。
func TestPropM3U8FileNameIsSafeFilename(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		withQuery, _, _ := drawM3U8URL(rt)
		now := time.Unix(int64(rapid.IntRange(0, 2_000_000_000).Draw(rt, "unix")), 0).UTC()
		got := m3u8FileName(withQuery, now)
		if got == "" {
			rt.Fatalf("m3u8 URL なのに保存名が空: %q", withQuery)
		}
		if s := sanitizeFilename(got); s != got {
			rt.Fatalf("m3u8FileName(%q) = %q は sanitizeFilename で %q に変わる", withQuery, got, s)
		}
		if strings.ContainsAny(got, `/\:*?"<>|`) {
			rt.Fatalf("m3u8FileName(%q) = %q に禁止文字が含まれる", withQuery, got)
		}
	})
}

// プロパティ（クエリ非依存 + 機微情報）: 保存名はクエリに依存せず、トークンを含まない。
func TestPropM3U8FileNameExcludesQuery(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		withQuery, withoutQuery, token := drawM3U8URL(rt)
		now := time.Unix(int64(rapid.IntRange(0, 2_000_000_000).Draw(rt, "unix")), 0).UTC()
		a, b := m3u8FileName(withQuery, now), m3u8FileName(withoutQuery, now)
		if a != b {
			rt.Fatalf("保存名がクエリに依存している: withQuery=%q withoutQuery=%q", a, b)
		}
		if strings.Contains(a, token) {
			rt.Fatalf("m3u8FileName(%q) = %q にトークンが漏れている", withQuery, a)
		}
	})
}

// プロパティ（冪等）: redactLine は二重適用しても結果が変わらない。
// ログ経路が増えて二重に通っても壊れないことを保証する。
func TestPropRedactLineIdempotent(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		line := genLogLine().Draw(rt, "line")
		once := redactLine(line)
		if twice := redactLine(once); twice != once {
			rt.Fatalf("冪等でない: line=%q once=%q twice=%q", line, once, twice)
		}
	})
}

// プロパティ（マスクの網羅）: クエリ付き URL を含む行を通すと、トークンが出力に残らない。
func TestPropRedactLineMasksToken(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		withQuery, _, token := drawM3U8URL(rt)
		prefix := rapid.SampledFrom([]string{
			"", "[STEP2] command: yt-dlp -- ", "[STEP3] yt-dlp: [download] Destination: ",
			"ERROR: Unable to download webpage: ",
		}).Draw(rt, "prefix")
		got := redactLine(prefix + withQuery)
		if strings.Contains(got, token) {
			rt.Fatalf("redactLine(%q) = %q にトークンが残っている", prefix+withQuery, got)
		}
		if !strings.Contains(got, redactedQuery) {
			rt.Fatalf("redactLine(%q) = %q がマスクされていない", prefix+withQuery, got)
		}
	})
}

// genLogLine は URL を含む行・含まない行の両方を生成する。
func genLogLine() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		switch rapid.IntRange(0, 2).Draw(t, "kind") {
		case 0:
			w, _, _ := drawM3U8URL(t)
			return rapid.SampledFrom([]string{"", "cmd -- ", "ERROR: "}).Draw(t, "prefix") + w
		case 1:
			_, wo, _ := drawM3U8URL(t)
			return "no query: " + wo
		default:
			return rapid.SampledFrom([]string{
				"[download]  45.3% of   10.00MiB at    1.50MiB/s ETA 00:03",
				"[hlsnative] Total fragments: 3", "", "   ", "no url here at all",
			}).Draw(t, "plain")
		}
	})
}

// プロパティ（保存）: URL を含まない行は一切変化しない。
func TestPropRedactLinePreservesLinesWithoutURL(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// URL になりえない文字集合だけで組んだ行
		line := rapid.StringMatching(`[a-zA-Z0-9 .%\[\]:/-]{0,60}`).Draw(rt, "line")
		if strings.Contains(line, "http") {
			rt.Skip("URL を含みうる行はこのプロパティの対象外")
		}
		if got := redactLine(line); got != line {
			rt.Fatalf("URL を含まない行が変化した: in=%q out=%q", line, got)
		}
	})
}

// --- プロセスツリー停止のプロパティ ---
// 仕様: design.md「プロセス管理（停止は孫プロセスまで及ばせる）」テスト可能プロパティ（PBT-01）

// プロパティ（範囲制約）: 任意の int に対し、停止対象として受理するのは pid > 1 のときだけ。
// 0 / 1 / 負数を 1 件でも受理すると kill(0,...) や kill(-1,...) が成立し、
// 無関係なプロセス（アプリ自身を含む）を殺しうる。
func TestPropIsKillablePIDRejectsDangerousValues(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		pid := rapid.OneOf(
			rapid.IntRange(-5, 5), // 危険な境界を厚めに
			rapid.Int(),           // 極端な値
			rapid.IntRange(2, 1<<22),
		).Draw(rt, "pid")

		if got, want := isKillablePID(pid), pid > 1; got != want {
			rt.Fatalf("isKillablePID(%d) = %v, want %v", pid, got, want)
		}
		// 反転して安全に使えるのは受理された場合だけ、を明示的に確認する。
		if isKillablePID(pid) && -pid >= -1 {
			rt.Fatalf("pid=%d を受理したが -pid=%d は危険な値（-1 以上）", pid, -pid)
		}
	})
}

// --- 登録拒否の報告と Referer 指定のプロパティ ---
// 仕様: design.md「拒否した理由を返す（沈黙させない）」「Referer のユーザー指定（元ページ URL）」

// genQueuedItemsWithURLs は URL を持つアイテム列を生成する（重複判定の対象になる集合）。
func genQueuedItemsWithURLs() *rapid.Generator[[]*DownloadItem] {
	return rapid.Custom(func(t *rapid.T) []*DownloadItem {
		n := rapid.IntRange(0, 6).Draw(t, "n")
		items := make([]*DownloadItem, 0, n)
		for i := 0; i < n; i++ {
			auth := genAuthority().Draw(t, "authority")
			path := rapid.StringMatching(`/[a-z0-9/_-]{0,12}`).Draw(t, "path")
			items = append(items, &DownloadItem{
				ID:     rapid.StringMatching(`[0-9]{1,4}`).Draw(t, "id"),
				URL:    "https://" + auth + path,
				Status: genStatus().Draw(t, "status"),
			})
		}
		return items
	})
}

// プロパティ（判定の一致 / Oracle）: 受理するのは「妥当な URL かつ未登録」のときだけ。
// 崩れると、登録できるはずの URL を拒否したり、不正な URL を受理したりする。
func TestPropAddRejectionMatchesOracle(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		items := genQueuedItemsWithURLs().Draw(rt, "items")
		url := genAdversarialURLish().Draw(rt, "url")

		got := addRejection(items, url)

		var want string
		switch {
		case !isValidURL(url):
			want = "invalid"
		case containsURL(items, url):
			want = "duplicate"
		}
		if got != want {
			rt.Fatalf("addRejection(%q) = %q, want %q", url, got, want)
		}
	})
}

// プロパティ（不変条件）: 拒否したときは必ず文言が付く。
// 「黙って消える」ことを構造的に防ぐのがこの関数の存在理由なので、空文言は許されない。
func TestPropAddRejectionAlwaysExplained(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		reason := rapid.OneOf(
			rapid.SampledFrom([]string{"", "invalid", "duplicate"}),
			rapid.StringMatching(`[a-z-]{1,12}`), // 将来追加される理由
		).Draw(rt, "reason")

		msg := addRejectionMessage(reason)
		if reason == "" {
			if msg != "" {
				rt.Fatalf("受理（reason=\"\"）なのに文言が付いた: %q", msg)
			}
			return
		}
		if msg == "" {
			rt.Fatalf("reason=%q で文言が空（黙って消えてしまう）", reason)
		}
	})
}

// プロパティ（範囲制約）: 退避の判定は閾値だけで決まり、単調である。
func TestPropShouldRotateLogIsMonotone(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		size := rapid.OneOf(
			rapid.Int64Range(0, maxLogBytes*2),
			rapid.Int64(),
		).Draw(rt, "size")

		if got, want := shouldRotateLog(size), size >= maxLogBytes; got != want {
			rt.Fatalf("shouldRotateLog(%d) = %v, want %v", size, got, want)
		}
		// 単調性: 退避する大きさより大きければ必ず退避する。
		if shouldRotateLog(size) && size < math.MaxInt64 && !shouldRotateLog(size+1) {
			rt.Fatalf("size=%d で退避するのに %d で退避しない（単調でない）", size, size+1)
		}
	})
}

// プロパティ（引数インジェクション）: 任意の URL と任意の指定値に対し "-" 始まりを返さない。
// ここが崩れると --referer の引数が yt-dlp のオプションに化ける。
func TestPropEffectiveRefererNeverStartsWithDash(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		url := genAdversarialURLish().Draw(rt, "url")
		override := genAdversarialURLish().Draw(rt, "override")

		if got := effectiveReferer(url, override); strings.HasPrefix(got, "-") {
			rt.Fatalf("effectiveReferer(%q, %q) = %q: '-' 始まりは化ける", url, override, got)
		}
	})
}

// プロパティ（優先順位）: 妥当な指定値は必ず採用され、自動導出に勝つ。
func TestPropEffectiveRefererPrefersValidOverride(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		url := genAdversarialURLish().Draw(rt, "url")
		auth := genAuthority().Draw(rt, "authority")
		path := rapid.StringMatching(`/[a-z0-9/_-]{0,12}`).Draw(rt, "path")
		override := "https://" + auth + path

		if !isValidURL(override) {
			rt.Skip("生成した指定値が妥当でない")
		}
		if got := effectiveReferer(url, override); got != override {
			rt.Fatalf("effectiveReferer(%q, %q) = %q: 妥当な指定値が採用されていない", url, override, got)
		}
	})
}

// プロパティ（フォールバック）: 指定がない・不正なら自動導出と一致する。
func TestPropEffectiveRefererFallsBackToAuto(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		url := genAdversarialURLish().Draw(rt, "url")
		override := rapid.SampledFrom([]string{
			"", "   ", "-J", "--referer=http://evil/", "not-a-url",
			"file:///etc/passwd", "example.com/x", "ftp://e.com/x",
		}).Draw(rt, "override")

		if isValidURL(override) {
			rt.Skip("この指定値は妥当なので採用されるのが正しい")
		}
		if got, want := effectiveReferer(url, override), refererFor(url); got != want {
			rt.Fatalf("effectiveReferer(%q, %q) = %q, want %q（自動導出へ落ちるべき）", url, override, got, want)
		}
	})
}
