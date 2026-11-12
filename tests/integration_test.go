//go:build integration

package tests

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-sdk/camoufox"
	"github.com/go-sdk/core/codec/json"
	"github.com/go-sdk/core/testx"
	"github.com/mxschmitt/playwright-go"
)

const integrationOrigin = "http://camoufox.test"

func newIntegrationClient(t *testing.T) *camoufox.Client {
	t.Helper()
	if os.Getenv("CAMOUFOX_INTEGRATION_TESTS") != "1" {
		t.Skip("set CAMOUFOX_INTEGRATION_TESTS=1 to enable browser integration tests")
	}
	endpoint := os.Getenv("CAMOUFOX_TEST_ENDPOINT")
	if endpoint == "" {
		t.Fatal("CAMOUFOX_TEST_ENDPOINT is required")
	}
	client := camoufox.New(camoufox.WithEndpoint(endpoint), camoufox.WithConnectTimeout(15*time.Second))
	t.Cleanup(func() {
		testx.NoError(t, client.Close())
	})
	testx.NoError(t, client.Connect())
	testx.True(t, client.IsConnected())
	return client
}

func newIntegrationPage(t *testing.T, session *camoufox.Session) playwright.Page {
	t.Helper()
	// 路由在发出网络请求前提供测试页面，保持稳定的存储源且无需访问外部网站。
	testx.NoError(t, session.Context().Route(integrationOrigin+"/**", func(route playwright.Route) {
		if err := route.Fulfill(playwright.RouteFulfillOptions{
			Status:      playwright.Int(200),
			ContentType: playwright.String("text/html; charset=utf-8"),
			Body:        "<!doctype html><html><head><title>Camoufox SDK fixture</title></head><body>fixture</body></html>",
		}); err != nil {
			t.Error("could not fulfill integration test page")
		}
	}))
	page, err := session.Context().NewPage()
	testx.NoError(t, err)
	page.SetDefaultTimeout(10000)
	page.SetDefaultNavigationTimeout(10000)
	_, err = page.Goto(integrationOrigin + "/")
	testx.NoError(t, err)
	return page
}

func TestIntegrationConnectionAndIsolation(t *testing.T) {
	first := newIntegrationClient(t)
	second := newIntegrationClient(t)
	testx.NoError(t, first.Connect())
	firstSession, err := first.NewSession()
	testx.NoError(t, err)
	otherSession, err := first.NewSession()
	testx.NoError(t, err)
	secondSession, err := second.NewSession()
	testx.NoError(t, err)
	firstPage := newIntegrationPage(t, firstSession)
	otherPage := newIntegrationPage(t, otherSession)
	secondPage := newIntegrationPage(t, secondSession)
	_, err = firstPage.Evaluate(`() => localStorage.setItem("fixture", "first")`)
	testx.NoError(t, err)
	testx.NoError(t, firstSession.Context().AddCookies([]playwright.OptionalCookie{{Name: "fixture", Value: "first", URL: playwright.String(integrationOrigin)}}))
	for _, page := range []playwright.Page{otherPage, secondPage} {
		value, err := page.Evaluate(`() => localStorage.getItem("fixture")`)
		testx.NoError(t, err)
		testx.Nil(t, value)
		cookies, err := page.Context().Cookies()
		testx.NoError(t, err)
		testx.Empty(t, cookies)
	}
	testx.NoError(t, otherSession.Close())
	testx.NoError(t, otherSession.Close())
	testx.True(t, otherPage.IsClosed())
	value, err := firstPage.Evaluate(`() => localStorage.getItem("fixture")`)
	testx.NoError(t, err)
	testx.Equal(t, "first", value)
	testx.NoError(t, first.Close())
	testx.NoError(t, first.Close())
	testx.True(t, firstPage.IsClosed())
	testx.False(t, first.IsConnected())
	testx.True(t, second.IsConnected())
	value, err = secondPage.Evaluate(`() => document.title`)
	testx.NoError(t, err)
	testx.Equal(t, "Camoufox SDK fixture", value)
}

func TestIntegrationPageFileTransfer(t *testing.T) {
	client := newIntegrationClient(t)
	session, err := client.NewSession()
	testx.NoError(t, err)
	page := newIntegrationPage(t, session)
	const content = "camoufox file transfer fixture"
	testx.NoError(t, session.Context().Route(integrationOrigin+"/upload", func(route playwright.Route) {
		request := route.Request()
		postData, err := request.PostData()
		status, body := 200, "upload accepted"
		if err != nil || request.Method() != "POST" || !strings.Contains(postData, content) || !strings.Contains(postData, "fixture.txt") {
			status, body = 400, "invalid upload"
		}
		if err := route.Fulfill(playwright.RouteFulfillOptions{Status: playwright.Int(status), Body: body}); err != nil {
			t.Error("could not fulfill fixture upload")
		}
	}))
	testx.NoError(t, session.Context().Route(integrationOrigin+"/download", func(route playwright.Route) {
		if err := route.Fulfill(playwright.RouteFulfillOptions{
			Status: playwright.Int(200),
			Headers: map[string]string{
				"Content-Type":        "text/plain",
				"Content-Disposition": `attachment; filename="download.txt"`,
			},
			Body: content,
		}); err != nil {
			t.Error("could not fulfill fixture download")
		}
	}))
	testx.NoError(t, page.SetContent(`<form action="/upload" method="post" enctype="multipart/form-data">
		<input type="file" name="file"><button>Upload</button></form><a href="/download">Download</a>`))
	path := filepath.Join(t.TempDir(), "fixture.txt")
	testx.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	testx.NoError(t, page.Locator(`input[type="file"]`).SetInputFiles(path))
	testx.NoError(t, page.Locator("button").Click())
	testx.NoError(t, page.WaitForURL(integrationOrigin+"/upload"))
	body, err := page.Locator("body").InnerText()
	testx.NoError(t, err)
	testx.Equal(t, "upload accepted", strings.TrimSpace(body))
	_, err = page.Goto(integrationOrigin + "/")
	testx.NoError(t, err)
	testx.NoError(t, page.SetContent(`<a href="/download">Download</a>`))
	download, err := page.ExpectDownload(func() error { return page.Locator("a").Click() })
	testx.NoError(t, err)
	testx.Equal(t, "download.txt", download.SuggestedFilename())
	path = filepath.Join(t.TempDir(), "download.txt")
	testx.NoError(t, download.SaveAs(path))
	data, err := os.ReadFile(path)
	testx.NoError(t, err)
	testx.Equal(t, content, string(data))
}

func TestIntegrationStorageState(t *testing.T) {
	client := newIntegrationClient(t)
	session, err := client.NewSession()
	testx.NoError(t, err)
	page := newIntegrationPage(t, session)
	testx.NoError(t, session.Context().AddCookies([]playwright.OptionalCookie{{Name: "fixture", Value: "saved", URL: playwright.String(integrationOrigin)}}))
	_, err = page.Evaluate(`async () => {
		localStorage.setItem("fixture", "saved");
		const database = await new Promise((resolve, reject) => {
			const request = indexedDB.open("fixture", 1);
			request.onupgradeneeded = () => request.result.createObjectStore("values");
			request.onsuccess = () => resolve(request.result);
			request.onerror = () => reject(new Error("open fixture database"));
		});
		try {
			await new Promise((resolve, reject) => {
				const transaction = database.transaction("values", "readwrite");
				transaction.objectStore("values").put("stored", "key");
				transaction.oncomplete = () => resolve();
				transaction.onerror = () => reject(new Error("write fixture database"));
				transaction.onabort = () => reject(new Error("abort fixture database"));
			});
		} finally {
			database.close();
		}
	}`)
	testx.NoError(t, err)
	directory := t.TempDir()
	defaultPath := filepath.Join(directory, "default.json")
	testx.NoError(t, session.SaveState(defaultPath))
	defaultState, err := os.ReadFile(defaultPath)
	testx.NoError(t, err)
	state := struct {
		Origins []struct {
			IndexedDB []json.Value `json:"indexedDB"`
		} `json:"origins"`
	}{}
	testx.NoError(t, json.Unmarshal(defaultState, &state))
	testx.Len(t, state.Origins, 1)
	testx.Empty(t, state.Origins[0].IndexedDB)
	path := filepath.Join(directory, "private", "state.json")
	testx.NoError(t, session.SaveState(path, playwright.BrowserContextStorageStateOptions{IndexedDB: playwright.Bool(true)}))
	data, err := os.ReadFile(path)
	testx.NoError(t, err)
	testx.NoError(t, json.Unmarshal(data, &state))
	testx.Len(t, state.Origins, 1)
	testx.Len(t, state.Origins[0].IndexedDB, 1)
	fileInfo, err := os.Stat(path)
	testx.NoError(t, err)
	testx.Equal(t, os.FileMode(0o600), fileInfo.Mode().Perm())
	directoryInfo, err := os.Stat(filepath.Dir(path))
	testx.NoError(t, err)
	testx.Equal(t, os.FileMode(0o700), directoryInfo.Mode().Perm())
	testx.NoError(t, session.Close())
	restored, err := client.NewSession(playwright.BrowserNewContextOptions{StorageStatePath: playwright.String(path)})
	testx.NoError(t, err)
	restoredPage := newIntegrationPage(t, restored)
	cookies, err := restored.Context().Cookies()
	testx.NoError(t, err)
	testx.Len(t, cookies, 1)
	testx.Equal(t, "fixture", cookies[0].Name)
	testx.Equal(t, "saved", cookies[0].Value)
	value, err := restoredPage.Evaluate(`() => localStorage.getItem("fixture")`)
	testx.NoError(t, err)
	testx.Equal(t, "saved", value)
	value, err = restoredPage.Evaluate(`async () => {
		const database = await new Promise((resolve, reject) => {
			const request = indexedDB.open("fixture", 1);
			request.onsuccess = () => resolve(request.result);
			request.onerror = () => reject(new Error("open restored database"));
		});
		try {
			return await new Promise((resolve, reject) => {
				const request = database.transaction("values").objectStore("values").get("key");
				request.onsuccess = () => resolve(request.result);
				request.onerror = () => reject(new Error("read restored database"));
			});
		} finally {
			database.close();
		}
	}`)
	testx.NoError(t, err)
	testx.Equal(t, "stored", value)
}

func TestIntegrationStateFailures(t *testing.T) {
	client := newIntegrationClient(t)
	directory := t.TempDir()
	corruptPath := filepath.Join(directory, "corrupt.json")
	testx.NoError(t, os.WriteFile(corruptPath, []byte(`{"cookies":`), 0o600))
	for _, path := range []string{"", corruptPath, filepath.Join(directory, "missing.json")} {
		session, err := client.NewSession(playwright.BrowserNewContextOptions{StorageStatePath: playwright.String(path)})
		testx.ErrorIs(t, err, camoufox.ErrStateRestore)
		testx.Nil(t, session)
	}
	invalidPath := filepath.Join(directory, "invalid-state.json")
	testx.NoError(t, os.WriteFile(invalidPath, []byte(`{"cookies":[{}],"origins":[]}`), 0o600))
	session, err := client.NewSession()
	testx.NoError(t, err)
	browser := session.Context().Browser()
	before := len(browser.Contexts())
	failedSession, err := client.NewSession(playwright.BrowserNewContextOptions{StorageStatePath: playwright.String(invalidPath)})
	testx.ErrorIs(t, err, camoufox.ErrStateRestore)
	testx.Nil(t, failedSession)
	testx.Len(t, browser.Contexts(), before)
	page := newIntegrationPage(t, session)
	_, err = page.Evaluate(`() => localStorage.setItem("fixture", "retained")`)
	testx.NoError(t, err)
	path := filepath.Join(directory, "state.json")
	testx.NoError(t, session.SaveState(path))
	previous, err := os.ReadFile(path)
	testx.NoError(t, err)
	targetDirectory := filepath.Join(directory, "existing-directory")
	testx.NoError(t, os.Mkdir(targetDirectory, 0o700))
	testx.ErrorIs(t, session.SaveState(targetDirectory), camoufox.ErrStateSave)
	fileInfo, err := os.Stat(targetDirectory)
	testx.NoError(t, err)
	testx.True(t, fileInfo.IsDir())
	temporary, err := filepath.Glob(filepath.Join(directory, ".camoufox-state-*.json"))
	testx.NoError(t, err)
	testx.Empty(t, temporary)
	testx.NoError(t, session.Close())
	testx.ErrorIs(t, session.SaveState(path), camoufox.ErrSessionClosed)
	current, err := os.ReadFile(path)
	testx.NoError(t, err)
	testx.True(t, string(previous) == string(current))
}

func TestIntegrationReconnect(t *testing.T) {
	client := newIntegrationClient(t)
	session, err := client.NewSession()
	testx.NoError(t, err)
	page := newIntegrationPage(t, session)
	_, err = page.Evaluate(`() => localStorage.setItem("fixture", "reconnected")`)
	testx.NoError(t, err)
	path := filepath.Join(t.TempDir(), "state.json")
	testx.NoError(t, session.SaveState(path))
	disconnected := make(chan struct{})
	session.Context().Browser().OnDisconnected(func(playwright.Browser) {
		close(disconnected)
	})
	testx.NoError(t, session.Context().Browser().Close())
	select {
	case <-disconnected:
	case <-time.After(10 * time.Second):
		t.Fatal("remote connection did not close")
	}
	testx.False(t, client.IsConnected())
	testx.True(t, page.IsClosed())
	testx.ErrorIs(t, session.SaveState(path), camoufox.ErrSessionClosed)
	testx.NoError(t, client.Connect())
	restored, err := client.NewSession(playwright.BrowserNewContextOptions{StorageStatePath: playwright.String(path)})
	testx.NoError(t, err)
	restoredPage := newIntegrationPage(t, restored)
	value, err := restoredPage.Evaluate(`() => localStorage.getItem("fixture")`)
	testx.NoError(t, err)
	testx.Equal(t, "reconnected", value)
}

func TestIntegrationDisconnectInterruptsPendingCall(t *testing.T) {
	client := newIntegrationClient(t)
	session, err := client.NewSession()
	testx.NoError(t, err)
	page := newIntegrationPage(t, session)
	started := make(chan struct{}, 1)
	page.OnConsole(func(message playwright.ConsoleMessage) {
		if message.Text() == "pending fixture" {
			started <- struct{}{}
		}
	})
	result := make(chan error, 1)
	go func() {
		_, err := page.Evaluate(`() => {
			console.log("pending fixture");
			return new Promise(() => {});
		}`)
		result <- err
	}()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("test call did not reach the browser")
	}
	testx.NoError(t, client.Disconnect(ctx))
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("interrupted call returned success")
		}
	case <-ctx.Done():
		t.Fatal("pending call did not stop after disconnect")
	}
	testx.NoError(t, client.Connect())
	testx.True(t, client.IsConnected())
}
