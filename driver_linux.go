//go:build amd64 || arm64

package camoufox

import (
	"syscall"

	"github.com/go-sdk/core/errx"
)

// isRunning 与 release 共用锁，兼容内核未提供 pidfd 时的 PID 查询。
func (d *clientDriver) isRunning() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.process == nil {
		return false
	}
	err := d.process.Signal(syscall.Signal(0))
	return err == nil || errx.Is(err, syscall.EPERM)
}

// release 只释放独立查询句柄，子进程仍由 Playwright 回收。
func (d *clientDriver) release() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.process == nil {
		return nil
	}
	err := d.process.Release()
	d.process = nil
	return err
}
