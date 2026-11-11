package camoufox

import (
	"maps"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/go-sdk/core/errx"
	"github.com/mxschmitt/playwright-go"
)

const (
	defaultEndpoint       = "ws://127.0.0.1:3000/ws"
	defaultConnectTimeout = 30 * time.Second
)

type Option func(*Client)

// Client 管理一个本地驱动及其远程连接，必须通过 New 创建，创建后不可复制。
type Client struct {
	endpoint        string
	connectTimeout  time.Duration
	headers         map[string]string
	driverDirectory string

	mu         sync.Mutex
	driver     *clientDriver
	browser    playwright.Browser
	sessions   map[*Session]struct{}
	connecting *connectAttempt
	closed     bool
	closeErr   error
}

type clientDriver struct {
	*playwright.Playwright
	mu sync.Mutex
	// 独立持有查询句柄，子进程仍由 Playwright 回收。
	process *os.Process
}

type connectAttempt struct {
	done       chan struct{}
	cleanupErr error
}

// New 仅保存配置，不启动驱动、安装依赖或建立连接。
func New(opts ...Option) *Client {
	c := &Client{
		endpoint:       defaultEndpoint,
		connectTimeout: defaultConnectTimeout,
		sessions:       make(map[*Session]struct{}),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	return c
}

// WithEndpoint 设置完整的 WebSocket 地址，保留路径和查询参数，不自动追加 /ws。
func WithEndpoint(endpoint string) Option {
	return func(c *Client) {
		c.endpoint = endpoint
	}
}

// WithConnectTimeout 设置远程连接超时；零表示不限时，负数在 Connect 时返回错误。
func WithConnectTimeout(timeout time.Duration) Option {
	return func(c *Client) {
		c.connectTimeout = timeout
	}
}

// WithHeaders 设置 WebSocket 握手 Header；创建客户端时复制，不持有调用方的 map。
func WithHeaders(headers map[string]string) Option {
	return func(c *Client) {
		c.headers = maps.Clone(headers)
	}
}

// WithDriverDirectory 指定预先安装的驱动目录；空值遵循上游环境变量和缓存路径。
func WithDriverDirectory(directory string) Option {
	return func(c *Client) {
		c.driverDirectory = directory
	}
}

// Connect 启动预先安装的驱动并连接 Firefox 服务；已连接时直接返回。
// 断线后可显式重连，旧会话会被清理；Close 后需要新建 Client。
func (c *Client) Connect() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClientClosed
	}
	if c.connecting != nil {
		c.mu.Unlock()
		return ErrConnecting
	}
	if c.driver != nil && c.driver.isRunning() && c.browser != nil && c.browser.IsConnected() {
		c.mu.Unlock()
		return nil
	}
	endpoint, err := url.Parse(c.endpoint)
	if err != nil || endpoint.Hostname() == "" || (endpoint.Scheme != "ws" && endpoint.Scheme != "wss") || endpoint.User != nil || endpoint.Fragment != "" {
		c.mu.Unlock()
		return ErrInvalidEndpoint
	}
	if c.connectTimeout < 0 {
		c.mu.Unlock()
		return ErrInvalidOptions
	}
	attempt := &connectAttempt{done: make(chan struct{})}
	c.connecting = attempt
	sessions, browser, driver := c.detach()
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		close(attempt.done)
		c.connecting = nil
		c.mu.Unlock()
	}()
	attempt.cleanupErr = closeResources(sessions, browser, driver)
	if attempt.cleanupErr != nil {
		return attempt.cleanupErr
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return ErrClientClosed
	}
	pw, err := playwright.Run(&playwright.RunOptions{
		DriverDirectory:     c.driverDirectory,
		SkipInstallBrowsers: true,
	})
	if err != nil {
		return operationError(ErrDriverStart, err)
	}
	process, err := os.FindProcess(pw.Pid())
	if err != nil {
		attempt.cleanupErr = operationError(ErrDriverStop, pw.Stop())
		return errx.Join(ErrDriverStart, attempt.cleanupErr)
	}
	driver = &clientDriver{Playwright: pw, process: process}
	browser, err = driver.Firefox.Connect(c.endpoint, playwright.BrowserTypeConnectOptions{
		Headers: c.headers,
		Timeout: playwright.Float(float64(c.connectTimeout) / float64(time.Millisecond)),
	})
	if err != nil {
		attempt.cleanupErr = closeResources(nil, nil, driver)
		return errx.Join(operationError(ErrConnect, err), attempt.cleanupErr)
	}
	c.mu.Lock()
	if c.closed || !driver.isRunning() || !browser.IsConnected() {
		publishErr := ErrNotConnected
		if c.closed {
			publishErr = ErrClientClosed
		}
		c.mu.Unlock()
		attempt.cleanupErr = closeResources(nil, browser, driver)
		return errx.Join(publishErr, attempt.cleanupErr)
	}
	c.driver, c.browser = driver, browser
	c.mu.Unlock()
	return nil
}

// IsConnected 查询本地驱动及当前远程连接，不发起网络探测。
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.closed && c.driver != nil && c.driver.isRunning() && c.browser != nil && c.browser.IsConnected()
}

// Close 关闭本客户端会话、断开连接并停止驱动，不保存快照。
// 首次调用等待正在进行的 Connect 清理完成并合并清理错误；并发重复关闭不等待首次清理。
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		err := c.closeErr
		c.mu.Unlock()
		return err
	}
	c.closed = true
	sessions, browser, driver := c.detach()
	connecting := c.connecting
	c.mu.Unlock()
	err := closeResources(sessions, browser, driver)
	if connecting != nil {
		<-connecting.done
		err = errx.Join(err, connecting.cleanupErr)
	}
	c.mu.Lock()
	c.closeErr = err
	c.mu.Unlock()
	return err
}

// detach 只在持有 mu 时转移资源，远程清理在锁外执行。
func (c *Client) detach() ([]*Session, playwright.Browser, *clientDriver) {
	sessions := make([]*Session, 0, len(c.sessions))
	for session := range c.sessions {
		sessions = append(sessions, session)
	}
	browser, driver := c.browser, c.driver
	c.sessions = make(map[*Session]struct{})
	c.browser, c.driver = nil, nil
	return sessions, browser, driver
}

func closeResources(sessions []*Session, browser playwright.Browser, driver *clientDriver) error {
	var result error
	for _, session := range sessions {
		result = errx.Join(result, session.close(true))
	}
	// 驱动退出后不再向失效的协议管道发送关闭请求。
	if browser != nil && (driver == nil || driver.isRunning()) {
		result = errx.Join(result, operationError(ErrDisconnect, browser.Close()))
	}
	if driver != nil {
		result = errx.Join(result, operationError(ErrDriverStop, driver.Stop()))
		result = errx.Join(result, operationError(ErrDriverStop, driver.release()))
	}
	return result
}
