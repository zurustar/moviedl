//go:build !windows

package main

import (
	"fmt"
	"os/exec"
	"syscall"
)

// applyOSProcAttr は起動する子をプロセスグループのリーダーにする。
//
// **これが要点。** yt-dlp はライブ HLS のダウンロードや ffmpeg 結合で ffmpeg を
// 子プロセスとして起動する。グループを作らないと、yt-dlp へのシグナルは孫に届かず
// ffmpeg が孤児化して処理を続ける（以前はこの関数が no-op で、実際にそうなっていた）。
//
// Setpgid: true を付けた子は自分自身がグループリーダーになるため pgid == pid が成り立つ。
// 別途 Getpgid を引く必要はない（引くと、プロセスが既に消えていたときの分岐が増える）。
// 詳細は aidlc-docs/inception/application-design/design.md
// 「プロセス管理（停止は孫プロセスまで及ばせる）」を参照。
func applyOSProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// trackProcessTree は非 Windows では何もしない（Setpgid で追跡が完結している）。
// Windows ではここで Job Object へ割り当てる。
func trackProcessTree(cmd *exec.Cmd) {}

// releaseProcessTree は非 Windows では何もしない（解放すべきハンドルがない）。
func releaseProcessTree(cmd *exec.Cmd) {}

// signalProcessTree はプロセスグループ全体へシグナルを送る。
//
// 符号の反転前に isKillablePID で必ず弾く。kill(2) は -1 で「権限内の全プロセス」、
// 0 で「自分自身のプロセスグループ」を対象にするため、pid が 0 / 1 / 負数のときに
// 反転させるとアプリごと巻き込んで殺す。
// 詳細は design.md「Kill(-pid) の符号は致命的に危険」を参照。
func signalProcessTree(cmd *exec.Cmd, sig syscall.Signal) error {
	// 未起動・既に解放済みは「止めるものがない」として黙って成功にする
	// （既存の suspendProcess / resumeProcess と同じ寛容さを保つ）。
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	if !isKillablePID(pid) {
		return fmt.Errorf("停止対象の pid が不正です: %d", pid)
	}
	return syscall.Kill(-pid, sig)
}

// killProcessTree はプロセスツリー全体を強制終了する。
func killProcessTree(cmd *exec.Cmd) error { return signalProcessTree(cmd, syscall.SIGKILL) }

// suspendProcessTree はプロセスツリー全体を中断する（一時停止）。
func suspendProcessTree(cmd *exec.Cmd) error { return signalProcessTree(cmd, syscall.SIGSTOP) }

// resumeProcessTree はプロセスツリー全体を再開する。
func resumeProcessTree(cmd *exec.Cmd) error { return signalProcessTree(cmd, syscall.SIGCONT) }

// processTreeGone はツリーにプロセスが 1 つも残っていないかを返す。
//
// yt-dlp の更新は「実行ファイルを掴んでいるプロセスが全部消えた」ことを確かめてから
// 置き換える必要がある。リーダー（yt-dlp）が回収されても孫の ffmpeg が生きている間は
// false を返さなければならない。シグナル 0 をプロセスグループへ送り、ESRCH なら
// グループに誰も残っていない。
func processTreeGone(cmd *exec.Cmd) bool {
	if cmd == nil || cmd.Process == nil {
		return true
	}
	pid := cmd.Process.Pid
	if !isKillablePID(pid) {
		// 対象を特定できないものは「待つ理由がない」として消えた扱いにする。
		return true
	}
	return syscall.Kill(-pid, 0) != nil
}
