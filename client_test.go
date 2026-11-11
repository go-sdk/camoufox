package camoufox

import (
	"os"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-sdk/core/errx"
	"github.com/go-sdk/core/testx"
	"github.com/mxschmitt/playwright-go"
)

func TestNew(t *testing.T) {
	client := New(nil, WithDriverDirectory(t.TempDir()))
	testx.Equal(t, defaultEndpoint, client.endpoint)
	testx.Equal(t, defaultConnectTimeout, client.connectTimeout)
	testx.Nil(t, client.driver)
	testx.Nil(t, client.browser)
	testx.False(t, client.IsConnected())
	testx.NoError(t, client.Close())
}

func TestClientOptions(t *testing.T) {
	headers := map[string]string{"X-Test-Client": "original"}
	client := New(WithEndpoint("wss://browser.example.test/proxy/ws?client=one"), WithConnectTimeout(5*time.Second), WithHeaders(headers), WithDriverDirectory("driver"))
	headers["X-Test-Client"] = "changed"
	testx.Equal(t, "wss://browser.example.test/proxy/ws?client=one", client.endpoint)
	testx.Equal(t, 5*time.Second, client.connectTimeout)
	testx.Equal(t, "original", client.headers["X-Test-Client"])
	testx.Equal(t, "driver", client.driverDirectory)
	client.headers["X-Test-Client"] = "client"
	testx.Equal(t, "changed", headers["X-Test-Client"])
}

func TestConnectValidation(t *testing.T) {
	for _, endpoint := range []string{"", "%", "http://localhost/ws", "ws:///ws", "ws://user@localhost/ws", "ws://localhost/ws#fragment"} {
		t.Run(endpoint, func(t *testing.T) {
			client := New(WithEndpoint(endpoint), WithDriverDirectory(t.TempDir()))
			testx.ErrorIs(t, client.Connect(), ErrInvalidEndpoint)
			testx.Nil(t, client.driver)
			testx.NoError(t, client.Close())
		})
	}
	client := New(WithConnectTimeout(-time.Second), WithDriverDirectory(t.TempDir()))
	testx.ErrorIs(t, client.Connect(), ErrInvalidOptions)
	testx.NoError(t, client.Close())
}

func TestClientLifecycleWithoutConnection(t *testing.T) {
	client := New()
	session, err := client.NewSession()
	testx.ErrorIs(t, err, ErrNotConnected)
	testx.Nil(t, session)
	testx.NoError(t, client.Close())
	testx.NoError(t, client.Close())
	testx.False(t, client.IsConnected())
	testx.ErrorIs(t, client.Connect(), ErrClientClosed)
	session, err = client.NewSession()
	testx.ErrorIs(t, err, ErrClientClosed)
	testx.Nil(t, session)
}

func TestNewSessionOptions(t *testing.T) {
	client := New()
	_, err := client.NewSession(playwright.BrowserNewContextOptions{}, playwright.BrowserNewContextOptions{})
	testx.ErrorIs(t, err, ErrInvalidOptions)
	_, err = client.NewSession(playwright.BrowserNewContextOptions{
		StorageStatePath: playwright.String("state.json"),
		StorageState:     &playwright.OptionalStorageState{},
	})
	testx.ErrorIs(t, err, ErrInvalidOptions)
}

func TestOperationError(t *testing.T) {
	detail := errx.New("upstream detail")
	for _, category := range []error{nil, playwright.ErrTimeout, playwright.ErrTargetClosed} {
		result := operationError(ErrConnect, errx.Join(detail, category))
		testx.ErrorIs(t, result, ErrConnect)
		testx.False(t, errx.Is(result, detail))
		testx.False(t, strings.Contains(result.Error(), detail.Error()))
		if category != nil {
			testx.ErrorIs(t, result, category)
		}
	}
	testx.Nil(t, operationError(ErrConnect, nil))
}

func TestDriverProcessState(t *testing.T) {
	process, err := os.FindProcess(os.Getpid())
	testx.NoError(t, err)
	driver := &clientDriver{process: process}
	t.Cleanup(func() { testx.NoError(t, driver.release()) })
	testx.True(t, driver.isRunning())
	testx.NoError(t, driver.release())
	testx.False(t, driver.isRunning())
	client := New()
	client.driver = driver
	// 未关闭的上游对象不能掩盖本地驱动失效。
	client.browser = &connectedBrowser{}
	testx.False(t, client.IsConnected())
	session, err := client.NewSession()
	testx.Nil(t, session)
	testx.ErrorIs(t, err, ErrNotConnected)
	old := &Session{client: client, driver: client.driver}
	client.sessions[old] = struct{}{}
	testx.ErrorIs(t, old.SaveState("unused.json"), ErrSessionClosed)
	testx.NoError(t, old.Close())
	testx.Empty(t, client.sessions)
}

func TestDriverConcurrentRelease(t *testing.T) {
	process, err := os.FindProcess(os.Getpid())
	testx.NoError(t, err)
	driver := &clientDriver{process: process}
	t.Cleanup(func() { testx.NoError(t, driver.release()) })
	start := make(chan struct{})
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			<-start
			for range 100 {
				driver.isRunning()
			}
		})
	}
	workers.Go(func() {
		<-start
		if err := driver.release(); err != nil {
			t.Error(err)
		}
	})
	close(start)
	workers.Wait()
	testx.False(t, driver.isRunning())
	testx.NoError(t, driver.release())
}

func TestCloseDuringConnectCleanup(t *testing.T) {
	for _, cleanupErr := range []error{nil, errx.New("upstream secret")} {
		name := "success"
		if cleanupErr != nil {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				browser := &cleanupBrowser{started: make(chan struct{}), proceed: make(chan struct{}), err: cleanupErr}
				release := sync.OnceFunc(func() { close(browser.proceed) })
				defer release()
				client := New()
				client.browser = browser
				connected := make(chan error, 1)
				go func() { connected <- client.Connect() }()
				<-browser.started
				closed := make(chan error, 1)
				go func() { closed <- client.Close() }()
				// 等待 Close 阻塞在连接清理上，再验证它尚未返回。
				synctest.Wait()
				testx.True(t, client.closed)
				select {
				case err := <-closed:
					t.Fatalf("client close returned before connection cleanup: %v", err)
				default:
				}
				release()
				for _, result := range []<-chan error{connected, closed} {
					err := <-result
					if cleanupErr != nil {
						testx.ErrorIs(t, err, ErrDisconnect)
						testx.NotContains(t, err.Error(), cleanupErr.Error())
					} else if result == connected {
						testx.ErrorIs(t, err, ErrClientClosed)
					} else {
						testx.NoError(t, err)
					}
				}
				if cleanupErr != nil {
					testx.ErrorIs(t, client.Close(), ErrDisconnect)
				} else {
					testx.NoError(t, client.Close())
				}
			})
		})
	}
}

type connectedBrowser struct{ playwright.Browser }

func (*connectedBrowser) IsConnected() bool { return true }

type cleanupBrowser struct {
	playwright.Browser
	started chan struct{}
	proceed chan struct{}
	err     error
}

func (*cleanupBrowser) IsConnected() bool { return false }

func (b *cleanupBrowser) Close(...playwright.BrowserCloseOptions) error {
	close(b.started)
	<-b.proceed
	return b.err
}
