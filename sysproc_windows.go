//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"
)

var (
	ntdll                    = syscall.NewLazyDLL("ntdll.dll")
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	ntSuspendProcess         = ntdll.NewProc("NtSuspendProcess")
	ntResumeProcess          = ntdll.NewProc("NtResumeProcess")
	openProcess              = kernel32.NewProc("OpenProcess")
	closeHandle              = kernel32.NewProc("CloseHandle")
	createJobObject          = kernel32.NewProc("CreateJobObjectW")
	assignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
	terminateJobObject       = kernel32.NewProc("TerminateJobObject")
	queryInformationJobObj   = kernel32.NewProc("QueryInformationJobObject")
)

const (
	processSuspendResume = 0x0800
	processTerminate     = 0x0001
	processSetQuota      = 0x0100

	// JobObjectBasicProcessIdList
	jobObjectBasicProcessIDList = 3

	// ジョブから列挙する pid の上限。yt-dlp + ffmpeg 程度なので十分な余裕。
	maxJobProcessIDs = 64
)

// jobHandles は *exec.Cmd に対応する Job Object のハンドルを保持する。
//
// Windows にはプロセスグループ相当の仕組みがないため、yt-dlp が起動する ffmpeg 孫プロセスを
// まとめて停止するには Job Object を使う。生成したハンドルは releaseProcessTree で閉じる。
var (
	jobMu      sync.Mutex
	jobHandles = map[*exec.Cmd]uintptr{}
)

// applyOSProcAttr は Windows でコンソールウィンドウが開くのを抑止する。
//
// HideWindow は**維持すること**。exec.Command で外部コマンドを起動すると Windows は既定で
// コンソールウィンドウを生成し、1 箇所でも漏れるとその呼び出し時にウィンドウが出る。
// design.md「Windows でコンソールウィンドウが一瞬開く問題」を参照。
//
// プロセスツリーの追跡は Start 前には行えないため trackProcessTree で行う。
func applyOSProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

// trackProcessTree は起動済みプロセスを新しい Job Object に割り当てる。
//
// **既知の競合窓:** 割り当ては cmd.Start() の直後に行うため、Start から Assign までの間に
// yt-dlp が子を産むと、その子はジョブに入らない。実際には yt-dlp（Python）の起動に時間がかかり
// ffmpeg を産むのはその後なので実用上は問題にならない。厳密に閉じるには CREATE_SUSPENDED で
// 起動してから割り当てて再開する必要があるが、os/exec はスレッドハンドルを公開しないため
// 現状の Go 標準ライブラリでは実装できない。
//
// 失敗しても致命的にはしない。ジョブが無ければツリー停止関数は
// **従来どおりの単一プロセス停止にフォールバック**するため、以前より悪くはならない。
func trackProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil || !isKillablePID(cmd.Process.Pid) {
		return
	}
	hJob, _, _ := createJobObject.Call(0, 0)
	if hJob == 0 {
		return
	}
	hProc, _, _ := openProcess.Call(processSetQuota|processTerminate, 0, uintptr(cmd.Process.Pid))
	if hProc == 0 {
		closeHandle.Call(hJob) //nolint:errcheck
		return
	}
	defer closeHandle.Call(hProc) //nolint:errcheck

	if ok, _, _ := assignProcessToJobObject.Call(hJob, hProc); ok == 0 {
		closeHandle.Call(hJob) //nolint:errcheck
		return
	}
	jobMu.Lock()
	jobHandles[cmd] = hJob
	jobMu.Unlock()
}

// releaseProcessTree はジョブハンドルを閉じる。Wait 後に呼ぶ。
func releaseProcessTree(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	jobMu.Lock()
	h, ok := jobHandles[cmd]
	delete(jobHandles, cmd)
	jobMu.Unlock()
	if ok && h != 0 {
		closeHandle.Call(h) //nolint:errcheck
	}
}

func jobFor(cmd *exec.Cmd) uintptr {
	jobMu.Lock()
	defer jobMu.Unlock()
	return jobHandles[cmd]
}

// jobProcessIDs はジョブに属するプロセスの pid を列挙する。
func jobProcessIDs(hJob uintptr) []int {
	// JOBOBJECT_BASIC_PROCESS_ID_LIST:
	//   DWORD NumberOfAssignedProcesses; DWORD NumberOfProcessIdsInList; ULONG_PTR ProcessIdList[1];
	// 64bit では 2 つの DWORD（計 8 バイト）の後、8 バイト境界から pid 配列が始まる。
	const headerSize = 8
	buf := make([]byte, headerSize+maxJobProcessIDs*int(unsafe.Sizeof(uintptr(0))))
	ok, _, _ := queryInformationJobObj.Call(
		hJob,
		jobObjectBasicProcessIDList,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		0,
	)
	if ok == 0 {
		return nil
	}
	count := int(*(*uint32)(unsafe.Pointer(&buf[4]))) // NumberOfProcessIdsInList
	if count < 0 || count > maxJobProcessIDs {
		count = maxJobProcessIDs
	}
	pids := make([]int, 0, count)
	for i := 0; i < count; i++ {
		off := headerSize + i*int(unsafe.Sizeof(uintptr(0)))
		pid := int(*(*uintptr)(unsafe.Pointer(&buf[off])))
		if isKillablePID(pid) {
			pids = append(pids, pid)
		}
	}
	return pids
}

// killProcessTree はジョブ全体を強制終了する。ジョブがなければ単一プロセスを Kill する。
func killProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if !isKillablePID(cmd.Process.Pid) {
		return fmt.Errorf("停止対象の pid が不正です: %d", cmd.Process.Pid)
	}
	if h := jobFor(cmd); h != 0 {
		if ok, _, _ := terminateJobObject.Call(h, 1); ok != 0 {
			return nil
		}
		// 失敗時は下のフォールバックへ落ちる。
	}
	return cmd.Process.Kill()
}

// suspendProcessTree はジョブ内の全プロセスを中断する。
func suspendProcessTree(cmd *exec.Cmd) error {
	return eachTreePID(cmd, suspendPID)
}

// resumeProcessTree はジョブ内の全プロセスを再開する。
func resumeProcessTree(cmd *exec.Cmd) error {
	return eachTreePID(cmd, resumePID)
}

// eachTreePID はツリーに属する各 pid に fn を適用する。
// ジョブが取れない場合は、従来どおり yt-dlp 本体の pid だけに適用する。
func eachTreePID(cmd *exec.Cmd, fn func(int) error) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	if !isKillablePID(pid) {
		return fmt.Errorf("停止対象の pid が不正です: %d", pid)
	}

	pids := []int{pid}
	if h := jobFor(cmd); h != 0 {
		if got := jobProcessIDs(h); len(got) > 0 {
			pids = got
		}
	}

	var firstErr error
	for _, p := range pids {
		if err := fn(p); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// processTreeGone はジョブにプロセスが 1 つも残っていないかを返す。
//
// yt-dlp の更新は「実行ファイルを掴んでいるプロセスが全部消えた」ことを確かめてから
// 置き換える必要がある（実行中の .exe は上書きできず共有違反になる）。
// yt-dlp 本体が終了していても ffmpeg 孫プロセスが掴んでいる間は false を返す。
func processTreeGone(cmd *exec.Cmd) bool {
	if cmd == nil || cmd.Process == nil {
		return true
	}
	if !isKillablePID(cmd.Process.Pid) {
		return true
	}
	if h := jobFor(cmd); h != 0 {
		return len(jobProcessIDs(h)) == 0
	}
	// ジョブが取れない場合は本体の生存だけで判定する（従来と同じ水準）。
	hProc, _, _ := openProcess.Call(processSuspendResume, 0, uintptr(cmd.Process.Pid))
	if hProc == 0 {
		return true
	}
	closeHandle.Call(hProc) //nolint:errcheck
	return false
}

func suspendPID(pid int) error { return callOnProcess(pid, ntSuspendProcess) }
func resumePID(pid int) error  { return callOnProcess(pid, ntResumeProcess) }

func callOnProcess(pid int, proc *syscall.LazyProc) error {
	if !isKillablePID(pid) {
		return fmt.Errorf("対象の pid が不正です: %d", pid)
	}
	h, _, _ := openProcess.Call(processSuspendResume, 0, uintptr(pid))
	if h == 0 {
		return syscall.EINVAL
	}
	defer closeHandle.Call(h) //nolint:errcheck
	proc.Call(h)              //nolint:errcheck
	return nil
}
