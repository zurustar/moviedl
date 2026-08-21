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
	"os/exec"
	"testing"

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
