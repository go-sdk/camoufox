package camoufox

import (
	"os"
	"path/filepath"

	"github.com/go-sdk/core/codec/json"
	"github.com/go-sdk/core/errx"
	"github.com/mxschmitt/playwright-go"
)

// SaveState 显式保存原始登录快照；可选原生参数支持 IndexedDB，Path 必须留空。
// 使用同目录 0600 临时文件替换目标；失败时保留旧快照，不在关闭时自动保存。
func (s *Session) SaveState(path string, options ...playwright.BrowserContextStorageStateOptions) (result error) {
	if path == "" {
		return errx.Join(ErrStateSave, ErrStatePath)
	}
	if len(options) > 1 {
		return ErrInvalidOptions
	}
	option := playwright.BrowserContextStorageStateOptions{}
	if len(options) == 1 {
		option = options[0]
	}
	if option.Path != nil {
		return ErrInvalidOptions
	}
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed || (s.driver != nil && !s.driver.isRunning()) || s.ctx.IsClosed() {
		return ErrSessionClosed
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return ErrStateSave
	}
	directory := filepath.Dir(target)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return ErrStateSave
	}
	file, err := os.CreateTemp(directory, ".camoufox-state-*.json")
	if err != nil {
		return ErrStateSave
	}
	temporary := file.Name()
	defer func() {
		if temporary != "" {
			if err := os.Remove(temporary); err != nil && !errx.Is(err, os.ErrNotExist) {
				result = errx.Join(result, ErrStateSave)
			}
		}
	}()
	if err := file.Close(); err != nil {
		return ErrStateSave
	}
	option.Path = playwright.String(temporary)
	if _, err := s.ctx.StorageState(option); err != nil {
		return operationError(ErrStateSave, err)
	}
	if _, err := readState(temporary); err != nil {
		return ErrStateSave
	}
	file, err = os.OpenFile(temporary, os.O_RDWR, 0)
	if err != nil {
		return ErrStateSave
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if syncErr != nil || closeErr != nil {
		return ErrStateSave
	}
	if err := os.Rename(temporary, target); err != nil {
		return ErrStateSave
	}
	temporary = ""
	return nil
}

// readState 只校验快照外层结构，不重建 JSON，保留 IndexedDB 等上游字段。
func readState(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	state := struct {
		Cookies []json.Value `json:"cookies"`
		Origins []json.Value `json:"origins"`
	}{}
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	if state.Cookies == nil || state.Origins == nil {
		return nil, ErrStateRestore
	}
	return data, nil
}

// SetStorageState 只接收路径，私有文件确保它读到已校验的内容。
func restoreState(ctx playwright.BrowserContext, data []byte) (result error) {
	file, err := os.CreateTemp("", ".camoufox-restore-*.json")
	if err != nil {
		return err
	}
	defer func() {
		if err := os.Remove(file.Name()); err != nil && !errx.Is(err, os.ErrNotExist) {
			result = errx.Join(result, err)
		}
	}()
	_, writeErr := file.Write(data)
	if err := errx.Join(writeErr, file.Close()); err != nil {
		return err
	}
	return ctx.SetStorageState(file.Name())
}
