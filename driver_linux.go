//go:build amd64 || arm64

package camoufox

import (
	"os"
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

// terminate 仅通过绑定进程身份的 pidfd 终止驱动，避免向复用的 PID 发信号。
func (d *clientDriver) terminate() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	process := d.process
	if process == nil {
		return nil
	}
	var signalErr error
	handleErr := process.WithHandle(func(uintptr) { signalErr = process.Kill() })
	if err := errx.Join(handleErr, signalErr); err != nil && !errx.Is(err, os.ErrProcessDone) {
		return err
	}
	err := process.Release()
	d.process = nil
	return err
}
