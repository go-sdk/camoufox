package camoufox

import (
	"github.com/go-sdk/core/errx"
	"github.com/mxschmitt/playwright-go"
)

var (
	ErrInvalidEndpoint = errx.New("invalid websocket endpoint")
	ErrInvalidOptions  = errx.New("invalid options")
	ErrClientClosed    = errx.New("client is closed")
	ErrConnecting      = errx.New("client is connecting")
	ErrNotConnected    = errx.New("client is not connected")
	ErrDriverStart     = errx.New("could not start playwright driver; prepare the matching driver first")
	ErrDriverStop      = errx.New("could not stop playwright driver")
	ErrConnect         = errx.New("could not connect to remote browser")
	ErrDisconnect      = errx.New("could not disconnect from remote browser")
	ErrNewSession      = errx.New("could not create browser context")
	ErrSessionClosed   = errx.New("session is closed")
	ErrSessionClose    = errx.New("could not close browser context")
	ErrStatePath       = errx.New("storage state path is required")
	ErrStateSave       = errx.New("could not save storage state")
	ErrStateRestore    = errx.New("could not restore storage state")
)

// operationError 只保留可安全判断的错误类别，避免上游调用日志泄露认证和快照内容。
func operationError(kind, cause error) error {
	if cause == nil {
		return nil
	}
	var category error
	if errx.Is(cause, playwright.ErrTimeout) {
		category = playwright.ErrTimeout
	} else if errx.Is(cause, playwright.ErrTargetClosed) {
		category = playwright.ErrTargetClosed
	}
	return errx.Join(kind, category)
}
