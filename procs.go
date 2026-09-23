// procs.go: yt-dlp プロセスの登録簿と停止（孫プロセスまで及ぼす）。OS 依存部は sysproc_*.go。

package main

import (
	"os/exec"
	"time"
)

// registerProc は yt-dlp プロセスを登録簿に載せる。yt-dlp を起こす直前に呼ぶ。
// 更新中は false を返すので、呼び出し側は起動せずに中止しなければならない。
func (a *App) registerProc(cmd *exec.Cmd) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.updating {
		return false
	}
	a.procs[cmd] = struct{}{}
	return true
}

// unregisterProc はプロセス終了後に登録簿から外す。registerProc が true を
// 返した経路では必ず（defer で）呼ぶこと。
func (a *App) unregisterProc(cmd *exec.Cmd) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.procs, cmd)
}

// liveProcCount は生存している yt-dlp プロセス数を返す。
func (a *App) liveProcCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.procs)
}

// beginUpdate / endUpdate は yt-dlp 実行ファイルの置き換え区間を囲む。
// この間は registerProc が false を返し、新規の yt-dlp 起動が止まる。
func (a *App) beginUpdate() {
	a.mu.Lock()
	a.updating = true
	a.mu.Unlock()
}

func (a *App) endUpdate() {
	a.mu.Lock()
	a.updating = false
	a.mu.Unlock()
	a.notify()
}

// stopTarget は更新のために停止する 1 アイテムを表す。
type stopTarget struct {
	item *DownloadItem
	// cmd はロック内で退避した item.cmd。STEP2a のタイトル取得中など、
	// アイテムに紐づくコマンドがまだ設定されていない場合は nil になる。
	cmd *exec.Cmd
	// needsResume はサスペンド中（一時停止）を表す。SIGSTOP 中のプロセスは
	// SIGKILL を受け取れないため、resume してから Kill する必要がある。
	needsResume bool
}

// planStop は更新のために停止すべきアイテムを、リストの順序を保って返す。
// 状態は変更しない（変更は呼び出し側の責務）。
// item.cmd を読むため **呼び出し側が a.mu を保持していること**。
// design.md「停止対象は『実行中』だけでは足りない」参照。
func planStop(items []*DownloadItem) []stopTarget {
	var out []stopTarget
	for _, it := range items {
		switch it.Status {
		case "downloading":
			out = append(out, stopTarget{item: it, cmd: it.cmd})
		case "paused":
			out = append(out, stopTarget{item: it, cmd: it.cmd, needsResume: true})
		}
	}
	return out
}

// stopTargets は planStop が選んだアイテムのプロセスを停止する。
// サスペンド中は SIGKILL を受け取れないため resume してから Kill する
// （CancelDownload と同じ順序）。
//
// 停止はプロセス**ツリー**単位で行う。yt-dlp だけを止めると ffmpeg が孤児化して
// 実行ファイルを掴んだままになり、Windows では置き換えが共有違反で失敗する。
// design.md「プロセス管理（停止は孫プロセスまで及ばせる）」を参照。
func stopTargets(targets []stopTarget) {
	for _, t := range targets {
		if t.cmd == nil || t.cmd.Process == nil {
			continue
		}
		if t.needsResume {
			resumeProcessTree(t.cmd) //nolint:errcheck
		}
		killProcessTree(t.cmd) //nolint:errcheck
	}
}

// killRegisteredProcs は登録簿にあるすべての yt-dlp プロセスを Kill する。
// FetchPlaylist / STEP2a のタイトル取得はアイテム経由で停止できないため、
// これが取りこぼしを防ぐ backstop になる。
// 戻り値は停止を試みたコマンド。呼び出し側が waitTreesGone で
// 孫プロセスまで消えたことを確認できるようにするため返す。
func (a *App) killRegisteredProcs() []*exec.Cmd {
	a.mu.Lock()
	cmds := make([]*exec.Cmd, 0, len(a.procs))
	for c := range a.procs {
		cmds = append(cmds, c)
	}
	a.mu.Unlock()
	for _, c := range cmds {
		if c.Process != nil {
			// ツリー単位で止める。yt-dlp だけを Kill すると ffmpeg が孤児化し、
			// waitProcsDrained が「排出完了」と誤判定したまま実行ファイルを掴み続ける。
			killProcessTree(c) //nolint:errcheck
		}
	}
	return cmds
}

// waitTreesGone は停止したプロセスツリーが 1 つも残らなくなるまで待つ。
//
// **なぜ登録簿の排出だけでは足りないか:** 登録簿が数えるのは yt-dlp プロセスであって、
// yt-dlp が起動する ffmpeg 孫プロセスではない。yt-dlp が回収された後も ffmpeg が
// 実行ファイルを掴んだままなら、Windows では os.Rename での置き換えが共有違反で失敗する。
// design.md「プロセス管理（停止は孫プロセスまで及ばせる）」を参照。
func waitTreesGone(cmds []*exec.Cmd, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		remaining := false
		for _, c := range cmds {
			if !processTreeGone(c) {
				remaining = true
				break
			}
		}
		if !remaining {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitProcsDrained は登録簿が空になるまで待ち、空になれば true を返す。
func (a *App) waitProcsDrained(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if a.liveProcCount() == 0 {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// isKillablePID は停止対象として正当な pid かを返す。1 以下を拒否する。
//
// **なぜ独立した述語にするか:** Unix の kill(2) は第 1 引数の符号と値で意味が変わる。
//
//	pid > 0        → そのプロセスのみ
//	-pgid (pgid>1) → そのプロセスグループの全員 ← これが狙い
//	-1             → 呼び出し元が権限を持つ全プロセス ← アプリごと巻き込んで殺す
//	0              → 呼び出し元自身のプロセスグループ ← アプリ自身を殺す
//
// cmd.Process が異常な pid を持つ経路で符号を反転させると kill(0, ...) や kill(-1, ...) が
// 成立しうる。requirements.md のセキュリティ要件「停止対象をそのダウンロードのために
// 起動したプロセスに限定する」に直結するため、反転の前に必ずここを通す。
// Windows 側でもジョブから列挙した pid の妥当性確認に使う。
// 詳細は aidlc-docs/inception/application-design/design.md「Kill(-pid) の符号は致命的に危険」を参照。
func isKillablePID(pid int) bool { return pid > 1 }
