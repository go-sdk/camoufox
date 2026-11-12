//go:build integration && linux

package tests

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/go-sdk/camoufox"
	"github.com/go-sdk/core/testx"
	"github.com/mxschmitt/playwright-go"
)

func TestIntegrationDriverExit(t *testing.T) {
	client, process := newDriverTestClient(t)
	session, err := client.NewSession()
	testx.NoError(t, err)
	page := newIntegrationPage(t, session)
	_, err = page.Evaluate(`() => localStorage.setItem("fixture", "driver-restarted")`)
	testx.NoError(t, err)
	path := filepath.Join(t.TempDir(), "state.json")
	testx.NoError(t, session.SaveState(path))
	// 仅终止本次 Connect 新建的子进程，不访问 SDK 私有状态。
	testx.NoError(t, process.Kill())
	waitForDriverExit(t, process.Pid)
	testx.False(t, client.IsConnected())
	_, err = client.NewSession()
	testx.ErrorIs(t, err, camoufox.ErrNotConnected)
	testx.ErrorIs(t, session.SaveState(path), camoufox.ErrSessionClosed)
	testx.NoError(t, session.Close())
	testx.NoError(t, client.Connect())
	restored, err := client.NewSession(playwright.BrowserNewContextOptions{StorageStatePath: playwright.String(path)})
	testx.NoError(t, err)
	value, err := newIntegrationPage(t, restored).Evaluate(`() => localStorage.getItem("fixture")`)
	testx.NoError(t, err)
	testx.Equal(t, "driver-restarted", value)
}

func TestIntegrationRecoveryAfterDisconnectTimeout(t *testing.T) {
	for _, tc := range []struct {
		name   string
		finish func(*camoufox.Client) error
	}{
		{"reconnect", (*camoufox.Client).Connect},
		{"close", (*camoufox.Client).Close},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, process := newDriverTestClient(t)
			if err := process.WithHandle(func(uintptr) {}); err != nil {
				t.Skip("forced driver cleanup requires pidfd")
			}
			session, err := client.NewSession()
			testx.NoError(t, err)
			testx.NoError(t, process.Signal(syscall.SIGSTOP))
			// 断言失败时先恢复驱动，避免测试清理卡在已暂停的子进程。
			t.Cleanup(func() { _ = process.Signal(syscall.SIGCONT) })

			ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
			defer cancel()
			testx.ErrorIs(t, client.Disconnect(ctx), context.DeadlineExceeded)
			finished := make(chan error, 1)
			go func() { finished <- tc.finish(client) }()
			select {
			case err := <-finished:
				testx.NoError(t, err)
			case <-time.After(10 * time.Second):
				t.Fatal("client did not recover while its old driver was paused")
			}
			waitForDriverExit(t, process.Pid)
			testx.ErrorIs(t, session.SaveState(filepath.Join(t.TempDir(), "unused.json")), camoufox.ErrSessionClosed)
			if tc.name == "reconnect" {
				replacement, err := client.NewSession()
				testx.NoError(t, err)
				value, err := newIntegrationPage(t, replacement).Title()
				testx.NoError(t, err)
				testx.Equal(t, "Camoufox SDK fixture", value)
			} else {
				_, err := client.NewSession()
				testx.ErrorIs(t, err, camoufox.ErrClientClosed)
			}
		})
	}
}

func newDriverTestClient(t *testing.T) (*camoufox.Client, *os.Process) {
	t.Helper()
	if os.Getenv("CAMOUFOX_INTEGRATION_TESTS") != "1" {
		t.Skip("set CAMOUFOX_INTEGRATION_TESTS=1 to enable browser integration tests")
	}
	before := childProcesses(t)
	client := newIntegrationClient(t)
	var created []int
	for pid := range childProcesses(t) {
		if _, exists := before[pid]; !exists {
			created = append(created, pid)
		}
	}
	testx.Len(t, created, 1)
	process, err := os.FindProcess(created[0])
	testx.NoError(t, err)
	t.Cleanup(func() { _ = process.Release() })
	return client, process
}

func waitForDriverExit(t *testing.T, pid int) {
	t.Helper()
	path := filepath.Join("/proc", strconv.Itoa(pid))
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		_, err := os.Stat(path)
		if os.IsNotExist(err) {
			return
		}
		testx.NoError(t, err)
		if time.Now().After(deadline) {
			t.Fatal("driver exit was not observed")
		}
	}
}

// childProcesses 只读取 Linux 进程元数据，不读取命令行和环境变量。
func childProcesses(t *testing.T) map[int]struct{} {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	testx.NoError(t, err)
	children := make(map[int]struct{})
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if err != nil {
			continue
		}
		_, rest, ok := strings.Cut(string(data), ") ")
		fields := strings.Fields(rest)
		if ok && len(fields) > 1 && fields[1] == strconv.Itoa(os.Getpid()) {
			children[pid] = struct{}{}
		}
	}
	return children
}
