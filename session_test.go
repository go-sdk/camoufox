package camoufox

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-sdk/core/errx"
	"github.com/go-sdk/core/testx"
	"github.com/mxschmitt/playwright-go"
)

func TestNewSessionRestoresValidatedState(t *testing.T) {
	directory := t.TempDir()
	path, replacement := filepath.Join(directory, "state.json"), filepath.Join(directory, "replacement.json")
	state := []byte(`{"cookies":[],"origins":[{"origin":"https://fixture.test","indexedDB":[]}],"future":true}` + "\n")
	testx.NoError(t, os.WriteFile(path, state, 0o600))
	testx.NoError(t, os.WriteFile(replacement, []byte(`{"cookies":[],"origins":[]}`), 0o600))

	var restoredPath string
	ctx := &sessionTestContext{restore: func(path string) error {
		restoredPath = path
		info, err := os.Stat(path)
		testx.NoError(t, err)
		testx.Equal(t, os.FileMode(0o600), info.Mode().Perm())
		actual, err := os.ReadFile(path)
		testx.NoError(t, err)
		testx.Equal(t, state, actual)
		return nil
	}}
	proxy := &playwright.Proxy{Server: "http://proxy.example.test:8080"}
	client := newSessionTestClient(t, &sessionTestBrowser{create: func(option playwright.BrowserNewContextOptions) (playwright.BrowserContext, error) {
		testx.Nil(t, option.StorageStatePath)
		testx.Same(t, proxy, option.Proxy)
		// 模拟另一任务在校验完成后发布新快照。
		testx.NoError(t, os.Rename(replacement, path))
		return ctx, nil
	}})
	session, err := client.NewSession(playwright.BrowserNewContextOptions{Proxy: proxy, StorageStatePath: playwright.String(path)})
	testx.NoError(t, err)
	testx.Same(t, ctx, session.Context())
	testx.Len(t, client.sessions, 1)
	testx.NotEmpty(t, restoredPath)
	testx.NotEqual(t, path, restoredPath)
	_, err = os.Stat(restoredPath)
	testx.ErrorIs(t, err, os.ErrNotExist)
	ctx.emitClose()
	testx.Empty(t, client.sessions)
	testx.ErrorIs(t, session.SaveState(path), ErrSessionClosed)
}

func TestNewSessionRestoreFailureClosesContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	testx.NoError(t, os.WriteFile(path, []byte(`{"cookies":[],"origins":[]}`), 0o600))
	detail := errx.New("fixture snapshot secret")
	var restoredPath string
	var closed bool
	ctx := &sessionTestContext{
		restore: func(path string) error {
			restoredPath = path
			return errx.Join(detail, playwright.ErrTimeout)
		},
		close: func() error {
			closed = true
			return detail
		},
	}
	client := newSessionTestClient(t, &sessionTestBrowser{create: func(playwright.BrowserNewContextOptions) (playwright.BrowserContext, error) {
		return ctx, nil
	}})
	session, err := client.NewSession(playwright.BrowserNewContextOptions{StorageStatePath: playwright.String(path)})
	testx.Nil(t, session)
	testx.ErrorIs(t, err, ErrStateRestore)
	testx.ErrorIs(t, err, playwright.ErrTimeout)
	testx.ErrorIs(t, err, ErrSessionClose)
	testx.NotContains(t, err.Error(), detail.Error())
	testx.True(t, closed)
	testx.Empty(t, client.sessions)
	testx.NotEmpty(t, restoredPath)
	_, err = os.Stat(restoredPath)
	testx.ErrorIs(t, err, os.ErrNotExist)
}

func TestClientCloseWaitsForSession(t *testing.T) {
	for _, tc := range []struct {
		name     string
		driverUp bool
	}{
		{name: "close event precedes reply", driverUp: true},
		{name: "driver exits while waiting"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				started, proceed, disconnected := make(chan struct{}), make(chan struct{}), make(chan struct{})
				release := sync.OnceFunc(func() { close(proceed) })
				defer release()
				detail := errx.New("fixture close failure")
				ctx := &sessionTestContext{close: func() error {
					close(started)
					<-proceed
					return detail
				}}
				client := newSessionTestClient(t, &sessionTestBrowser{
					create: func(playwright.BrowserNewContextOptions) (playwright.BrowserContext, error) { return ctx, nil },
					close:  func() error { close(disconnected); return nil },
				})
				session, err := client.NewSession()
				testx.NoError(t, err)
				// 只保留会话的查询句柄，客户端不持有真实驱动。
				client.driver = nil
				sessionClosed := make(chan error, 1)
				go func() { sessionClosed <- session.Close() }()
				<-started
				testx.NoError(t, session.Close())
				if tc.driverUp {
					ctx.emitClose()
					testx.Len(t, client.sessions, 1)
				}
				clientClosed := make(chan error, 1)
				go func() { clientClosed <- client.Close() }()
				synctest.Wait()
				select {
				case <-disconnected:
					t.Fatal("browser disconnected before session close finished")
				default:
				}
				if tc.driverUp {
					release()
					err := <-clientClosed
					testx.ErrorIs(t, err, ErrSessionClose)
					testx.NotContains(t, err.Error(), detail.Error())
				} else {
					testx.NoError(t, session.driver.release())
					select {
					case err := <-clientClosed:
						testx.NoError(t, err)
					case <-time.After(time.Second):
						t.Fatal("client still waiting after driver exited")
					}
					release()
				}
				<-disconnected
				testx.ErrorIs(t, <-sessionClosed, ErrSessionClose)
			})
		})
	}
}

func newSessionTestClient(t *testing.T, browser playwright.Browser) *Client {
	t.Helper()
	process, err := os.FindProcess(os.Getpid())
	testx.NoError(t, err)
	driver := &clientDriver{process: process}
	t.Cleanup(func() { testx.NoError(t, driver.release()) })
	client := New()
	client.browser, client.driver = browser, driver
	return client
}

type sessionTestBrowser struct {
	playwright.Browser
	create func(playwright.BrowserNewContextOptions) (playwright.BrowserContext, error)
	close  func() error
}

func (*sessionTestBrowser) IsConnected() bool { return true }

func (b *sessionTestBrowser) NewContext(options ...playwright.BrowserNewContextOptions) (playwright.BrowserContext, error) {
	return b.create(options[0])
}

func (b *sessionTestBrowser) Close(...playwright.BrowserCloseOptions) error { return b.close() }

type sessionTestContext struct {
	playwright.BrowserContext
	closed  atomic.Bool
	onClose func(playwright.BrowserContext)
	restore func(string) error
	close   func() error
}

func (c *sessionTestContext) IsClosed() bool { return c.closed.Load() }

func (c *sessionTestContext) OnClose(fn func(playwright.BrowserContext)) { c.onClose = fn }

func (c *sessionTestContext) emitClose() {
	c.closed.Store(true)
	c.onClose(c)
}

func (c *sessionTestContext) SetStorageState(path string) error { return c.restore(path) }

func (c *sessionTestContext) Close(...playwright.BrowserContextCloseOptions) error { return c.close() }
