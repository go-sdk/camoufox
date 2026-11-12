package camoufox

import (
	"sync"
	"time"

	"github.com/go-sdk/core/errx"
	"github.com/mxschmitt/playwright-go"
)

// Session 管理一个独立上下文及其页面，创建后不可复制。
type Session struct {
	client *Client
	ctx    playwright.BrowserContext
	driver *clientDriver

	mu            sync.Mutex
	saveMu        sync.Mutex
	closed        bool
	contextClosed bool
	closeDone     chan struct{}
	closeErr      error
}

// NewSession 创建上下文，支持零个或一个原生选项；代理由远程浏览器连接。
// StorageStatePath 通过原始 JSON 恢复，避免 Go 状态模型丢失 IndexedDB。
func (c *Client) NewSession(options ...playwright.BrowserNewContextOptions) (*Session, error) {
	if len(options) > 1 {
		return nil, ErrInvalidOptions
	}
	option := playwright.BrowserNewContextOptions{}
	if len(options) == 1 {
		option = options[0]
	}
	statePath := option.StorageStatePath
	if statePath != nil && option.StorageState != nil {
		return nil, ErrInvalidOptions
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrClientClosed
	}
	browser := c.browser
	driver := c.driver
	if c.disconnecting != nil || driver == nil || !driver.isRunning() || browser == nil || !browser.IsConnected() {
		c.mu.Unlock()
		return nil, ErrNotConnected
	}
	c.mu.Unlock()
	var state []byte
	if statePath != nil {
		if *statePath == "" {
			return nil, errx.Join(ErrStateRestore, ErrStatePath)
		}
		var err error
		if state, err = readState(*statePath); err != nil {
			return nil, ErrStateRestore
		}
		option.StorageStatePath = nil
	}
	ctx, err := browser.NewContext(option)
	if err != nil {
		return nil, operationError(ErrNewSession, err)
	}
	session := &Session{client: c, ctx: ctx, driver: driver}
	ctx.OnClose(func(playwright.BrowserContext) {
		session.mu.Lock()
		session.closed = true
		session.contextClosed = true
		closing := session.closeDone != nil
		session.mu.Unlock()
		if !closing {
			c.mu.Lock()
			delete(c.sessions, session)
			c.mu.Unlock()
		}
	})
	if statePath != nil {
		if err := restoreState(ctx, state); err != nil {
			return nil, errx.Join(operationError(ErrStateRestore, err), session.Close())
		}
	}
	c.mu.Lock()
	var publishErr error
	if c.closed {
		publishErr = ErrClientClosed
	} else if c.disconnecting != nil || c.browser != browser || !driver.isRunning() || !browser.IsConnected() {
		publishErr = ErrNotConnected
	} else if ctx.IsClosed() {
		publishErr = ErrSessionClosed
	} else {
		c.sessions[session] = struct{}{}
	}
	c.mu.Unlock()
	if publishErr != nil {
		return nil, errx.Join(publishErr, session.Close())
	}
	return session, nil
}

// Context 返回原生上下文，供调用方创建页面和使用完整 Playwright API。
// 关闭或断线后返回的旧对象失效，调用方应重新创建会话。
func (s *Session) Context() playwright.BrowserContext {
	return s.ctx
}

// Close 幂等关闭会话及其页面，不隐式保存登录快照。
func (s *Session) Close() error {
	return s.close(false)
}

func (s *Session) close(wait bool) error {
	s.mu.Lock()
	if s.closed {
		err, done := s.closeErr, s.closeDone
		s.mu.Unlock()
		if !wait || done == nil {
			return err
		}
		// 驱动退出时，上游已发出的关闭请求可能不再返回。
		var check <-chan time.Time
		if s.driver != nil {
			if !s.driver.isRunning() {
				return err
			}
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			check = ticker.C
		}
		for {
			select {
			case <-done:
				s.mu.Lock()
				err := s.closeErr
				s.mu.Unlock()
				return err
			case <-check:
				if !s.driver.isRunning() {
					return err
				}
			}
		}
	}
	s.closed = true
	done := make(chan struct{})
	s.closeDone = done
	s.mu.Unlock()
	var err error
	if s.driver == nil || s.driver.isRunning() {
		err = s.ctx.Close()
	}
	if errx.Is(err, playwright.ErrTargetClosed) {
		err = nil
	}
	err = operationError(ErrSessionClose, err)
	s.mu.Lock()
	s.closeErr = err
	s.closeDone = nil
	unregister := err == nil || s.contextClosed
	s.mu.Unlock()
	if unregister {
		s.client.mu.Lock()
		delete(s.client.sessions, s)
		s.client.mu.Unlock()
	}
	close(done)
	return err
}
