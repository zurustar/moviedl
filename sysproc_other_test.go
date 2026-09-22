//go:build !windows

// sysproc_other_test.go は非 Windows のプロセスツリー停止を、実プロセスを起こして検証する。
//
// 仕様: aidlc-docs/inception/application-design/design.md
// 「プロセス管理（停止は孫プロセスまで及ばせる）」
//
// 背景: yt-dlp はライブ HLS のダウンロードや ffmpeg 結合で **ffmpeg を子プロセスとして起動**する。
// 以前は applyOSProcAttr が非 Windows で no-op だったためプロセスグループが作られず、
// yt-dlp への SIGKILL が孫に届かず ffmpeg が孤児化して処理を続けていた。
// ここではその回帰を「子を産むプロセス」で再現して固定する。

package main

import (
	"bufio"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// startTreeProc は「子プロセスを 1 つ産んで待つ」プロセスを起動し、
// 親の *exec.Cmd と子の pid を返す。
//
// 非対話 sh は job control を行わないため、バックグラウンドの子は親と同じ
// プロセスグループに入る。yt-dlp が ffmpeg を起こすのと同じ構造。
func startTreeProc(t *testing.T) (*exec.Cmd, int) {
	t.Helper()

	cmd := exec.Command("/bin/sh", "-c", "sleep 300 & echo $!; wait")
	applyOSProcAttr(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("子の pid を読めない: %v", err)
	}
	childPid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatalf("子の pid が数値でない (%q): %v", line, err)
	}

	// テストが途中で失敗しても sleep を残さない。
	t.Cleanup(func() {
		syscall.Kill(childPid, syscall.SIGKILL) //nolint:errcheck
		if cmd.Process != nil {
			syscall.Kill(cmd.Process.Pid, syscall.SIGKILL) //nolint:errcheck
		}
		cmd.Wait() //nolint:errcheck
	})

	if !processAlive(childPid) {
		t.Fatalf("起動直後に子 pid=%d が生きていない", childPid)
	}
	return cmd, childPid
}

// processAlive はシグナル 0 の送信で pid の生存を確認する。
func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

// waitGone は pid が消えるまで待つ（孤児が reparent されて回収されるまでの猶予を含む）。
func waitGone(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return !processAlive(pid)
}

// procState は ps で取得したプロセス状態を返す（停止中は "T" を含む）。
func procState(t *testing.T, pid int) string {
	t.Helper()
	out, err := exec.Command("ps", "-o", "state=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// applyOSProcAttr がプロセスグループを作ることを固定する。
// これが no-op に戻ると、以下のツリー停止がすべて静かに効かなくなる。
func TestApplyOSProcAttrCreatesProcessGroup(t *testing.T) {
	cmd := exec.Command("true")
	applyOSProcAttr(cmd)

	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr が設定されていない（プロセスグループが作られない）")
	}
	if !cmd.SysProcAttr.Setpgid {
		t.Error("Setpgid が false: 孫プロセスが同じグループに入らず停止が届かない")
	}
}

// 回帰テスト: 以前は yt-dlp だけを Kill していたため ffmpeg が孤児化していた。
// killProcessTree は孫まで停止しなければならない。
func TestKillProcessTreeKillsDescendants(t *testing.T) {
	cmd, childPid := startTreeProc(t)

	if err := killProcessTree(cmd); err != nil {
		t.Fatalf("killProcessTree: %v", err)
	}
	cmd.Wait() //nolint:errcheck

	if !waitGone(childPid, 3*time.Second) {
		t.Errorf("子 pid=%d が生き残った（孤児化の回帰）", childPid)
	}
}

// 対照実験: リーダーだけを Kill すると子は生き残る。
// これが「なぜツリー停止が必要か」の実証であり、この前提が変わったら設計を見直す合図になる。
func TestKillingOnlyLeaderLeavesOrphan(t *testing.T) {
	cmd, childPid := startTreeProc(t)

	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	cmd.Wait() //nolint:errcheck

	// リーダーの終了後も子は動き続ける（孤児化）。
	time.Sleep(300 * time.Millisecond)
	if !processAlive(childPid) {
		t.Skip("この環境ではリーダーの終了で子も終了した（前提が異なる）")
	}

	// 後始末はツリー停止で行えることも確認する。
	if err := killProcessTree(cmd); err != nil {
		t.Fatalf("killProcessTree: %v", err)
	}
	if !waitGone(childPid, 3*time.Second) {
		t.Errorf("ツリー停止でも子 pid=%d を止められなかった", childPid)
	}
}

// 一時停止は孫まで及ばなければならない。
// 以前は yt-dlp だけが止まり、ffmpeg はダウンロードを続けていた。
func TestSuspendAndResumeProcessTreeAffectsDescendants(t *testing.T) {
	cmd, childPid := startTreeProc(t)

	if err := suspendProcessTree(cmd); err != nil {
		t.Fatalf("suspendProcessTree: %v", err)
	}
	// 状態が反映されるまで少し待つ。
	stopped := false
	for i := 0; i < 50; i++ {
		if strings.Contains(procState(t, childPid), "T") {
			stopped = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !stopped {
		t.Errorf("子 pid=%d が停止状態(T)にならない: state=%q", childPid, procState(t, childPid))
	}

	if err := resumeProcessTree(cmd); err != nil {
		t.Fatalf("resumeProcessTree: %v", err)
	}
	resumed := false
	for i := 0; i < 50; i++ {
		if !strings.Contains(procState(t, childPid), "T") {
			resumed = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !resumed {
		t.Errorf("子 pid=%d が再開しない: state=%q", childPid, procState(t, childPid))
	}
}

// 不正な pid では符号を反転させないこと（kill(0,...) / kill(-1,...) の防止）。
func TestProcessTreeStopRejectsInvalidPID(t *testing.T) {
	// Process が nil のコマンドは何もせずエラーも返さない（既存の suspendProcess と同じ寛容さ）。
	notStarted := exec.Command("true")
	for name, fn := range map[string]func(*exec.Cmd) error{
		"kill":    killProcessTree,
		"suspend": suspendProcessTree,
		"resume":  resumeProcessTree,
	} {
		if err := fn(notStarted); err != nil {
			t.Errorf("%s: 未起動のコマンドでエラーを返した: %v", name, err)
		}
		if err := fn(nil); err != nil {
			t.Errorf("%s: nil でエラーを返した: %v", name, err)
		}
	}

	// pid が危険な値のときはエラーを返して、シグナルを送らない。
	// ここを通してしまうと kill(0, ...) でアプリ自身、kill(-1, ...) で全プロセスを殺す。
	for _, pid := range []int{0, 1, -1} {
		for name, fn := range map[string]func(*exec.Cmd) error{
			"kill":    killProcessTree,
			"suspend": suspendProcessTree,
			"resume":  resumeProcessTree,
		} {
			bad := exec.Command("true")
			bad.Process = &os.Process{Pid: pid}
			if err := fn(bad); err == nil {
				t.Errorf("%s: pid=%d でエラーを返さなかった（危険な値を受理している）", name, pid)
			}
		}
	}
}

// yt-dlp の更新は「実行ファイルを掴んでいるプロセスが全部消えた」ことを確かめてから
// 置き換えなければならない。リーダーが消えても子が生きている間は「消えていない」と
// 判定できることを固定する（以前は yt-dlp の生存だけを見て排出完了と誤判定していた）。
func TestProcessTreeGoneDetectsSurvivingChild(t *testing.T) {
	cmd, childPid := startTreeProc(t)

	if processTreeGone(cmd) {
		t.Fatal("起動直後にツリーが消えたと判定された")
	}

	// リーダーだけを終了させる（従来の停止方法と同じ）。
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	cmd.Wait() //nolint:errcheck

	if !processAlive(childPid) {
		t.Skip("この環境ではリーダーの終了で子も終了した（前提が異なる）")
	}
	if processTreeGone(cmd) {
		t.Error("子が生き残っているのにツリーが消えたと判定した（排出完了の誤判定）")
	}

	// ツリー停止すれば消滅を検出できる。
	if err := killProcessTree(cmd); err != nil {
		t.Fatalf("killProcessTree: %v", err)
	}
	gone := false
	for i := 0; i < 150; i++ {
		if processTreeGone(cmd) {
			gone = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !gone {
		t.Errorf("ツリー停止後も消滅を検出できない（子 pid=%d alive=%v）", childPid, processAlive(childPid))
	}
}

// waitTreesGone は「停止したツリーが全部消えるまで」待つ。
// 子が生き残っている間は待ち続け、タイムアウトで false を返さなければならない。
func TestWaitTreesGone(t *testing.T) {
	cmd, childPid := startTreeProc(t)

	// 子が生きている間はタイムアウトする。
	if waitTreesGone([]*exec.Cmd{cmd}, 200*time.Millisecond) {
		t.Error("プロセスが生きているのに排出完了と判定した")
	}

	if err := killProcessTree(cmd); err != nil {
		t.Fatalf("killProcessTree: %v", err)
	}
	cmd.Wait() //nolint:errcheck

	if !waitTreesGone([]*exec.Cmd{cmd}, 3*time.Second) {
		t.Errorf("ツリー停止後に排出完了を検出できない（子 pid=%d alive=%v）", childPid, processAlive(childPid))
	}

	// 空リスト・nil 要素は待つ理由がないので即 true。
	if !waitTreesGone(nil, 0) {
		t.Error("空リストで false を返した")
	}
	if !waitTreesGone([]*exec.Cmd{nil, exec.Command("true")}, 0) {
		t.Error("未起動・nil を含むリストで false を返した")
	}
}
