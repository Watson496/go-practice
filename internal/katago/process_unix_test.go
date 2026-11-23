//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package katago

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func Test解析を中止すると起動した子プロセスも終了する(t *testing.T) {
	directory := t.TempDir()
	pidFile := directory + "/child.pid"
	wrapper := directory + "/katago-wrapper"
	script := fmt.Sprintf("#!/bin/sh\nsleep 30 &\necho $! > %q\nwait\n", pidFile)
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := analyze(ctx, Options{BinaryPath: wrapper}, query{ID: "cancel", BoardXSize: 5, BoardYSize: 5})
		result <- err
	}()
	var contents []byte
	deadline := time.Now().Add(30 * time.Second)
	for len(contents) == 0 && time.Now().Before(deadline) {
		contents, _ = os.ReadFile(pidFile)
		if len(contents) == 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if len(contents) == 0 {
		cancel()
		t.Fatal("子プロセスが起動しませんでした")
	}
	started := time.Now()
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("中止した解析のエラー = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("解析の中止に時間がかかりすぎました")
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("解析の中止に時間がかかりすぎました：%v", elapsed)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(contents)))
	if err != nil {
		t.Fatal(err)
	}
	terminationDeadline := time.Now().Add(3 * time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			break
		}
		if err != nil || time.Now().After(terminationDeadline) {
			t.Fatalf("子プロセス%dが残っています：%v", pid, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
