//go:build integration && linux

package tests

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-sdk/camoufox"
	"github.com/go-sdk/core/testx"
	"github.com/mxschmitt/playwright-go"
)

func TestIntegrationDriverExit(t *testing.T) {
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
	session, err := client.NewSession()
	testx.NoError(t, err)
	page := newIntegrationPage(t, session)
	_, err = page.Evaluate(`() => localStorage.setItem("fixture", "driver-restarted")`)
	testx.NoError(t, err)
	path := filepath.Join(t.TempDir(), "state.json")
	testx.NoError(t, session.SaveState(path))
	// 仅终止本次 Connect 新建的子进程，不访问 SDK 私有状态。
	testx.NoError(t, process.Kill())
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, err := os.Stat(filepath.Join("/proc", strconv.Itoa(created[0])))
		if os.IsNotExist(err) && !client.IsConnected() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("driver exit was not observed")
		}
		time.Sleep(10 * time.Millisecond)
	}
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
