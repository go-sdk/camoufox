package camoufox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-sdk/core/errx"
	"github.com/go-sdk/core/testx"
	"github.com/mxschmitt/playwright-go"
)

func TestReadState(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state string
		valid bool
	}{
		{name: "empty context", state: `{"cookies":[],"origins":[]}`, valid: true},
		{
			name:  "indexeddb and unknown fields",
			state: `{"cookies":[],"origins":[{"origin":"http://camoufox.test","indexedDB":[{"name":"fixture","version":1,"stores":[]}]}],"future":{"value":1}}`,
			valid: true,
		},
		{name: "invalid json", state: `{"cookies":`},
		{name: "missing cookies", state: `{"origins":[]}`},
		{name: "missing origins", state: `{"cookies":[]}`},
		{name: "null cookies", state: `{"cookies":null,"origins":[]}`},
		{name: "null origins", state: `{"cookies":[],"origins":null}`},
		{name: "invalid cookies", state: `{"cookies":{},"origins":[]}`},
		{name: "invalid origins", state: `{"cookies":[],"origins":{}}`},
		{name: "null root", state: `null`},
		{name: "array root", state: `[]`},
		{name: "trailing json", state: `{"cookies":[],"origins":[]} {}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			testx.NoError(t, os.WriteFile(path, []byte(tc.state), 0o600))
			data, err := readState(path)
			if tc.valid {
				testx.NoError(t, err)
				testx.Equal(t, tc.state, string(data))
			} else {
				testx.Error(t, err)
				testx.Nil(t, data)
			}
			current, err := os.ReadFile(path)
			testx.NoError(t, err)
			testx.Equal(t, tc.state, string(current))
		})
	}
	_, err := readState(filepath.Join(t.TempDir(), "missing.json"))
	testx.ErrorIs(t, err, os.ErrNotExist)
}

func TestSaveStateValidation(t *testing.T) {
	session := &Session{closed: true}
	testx.ErrorIs(t, session.SaveState(""), ErrStatePath)
	testx.ErrorIs(t, session.SaveState(""), ErrStateSave)
	testx.ErrorIs(t, session.SaveState("state.json", playwright.BrowserContextStorageStateOptions{}, playwright.BrowserContextStorageStateOptions{}), ErrInvalidOptions)
	testx.ErrorIs(t, session.SaveState("state.json", playwright.BrowserContextStorageStateOptions{Path: playwright.String("other.json")}), ErrInvalidOptions)
	testx.ErrorIs(t, session.SaveState("state.json"), ErrSessionClosed)
}

func TestSaveStateReplacesFile(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "private")
	path := filepath.Join(directory, "state.json")
	ctx := &storageContext{data: []byte(`{"cookies":[],"origins":[]}`)}
	session := &Session{ctx: ctx}
	testx.NoError(t, session.SaveState(path))

	ctx.data = []byte(`{"cookies":[],"origins":[{"origin":"https://fixture.test","indexedDB":[]}],"future":true}` + "\n")
	testx.NoError(t, session.SaveState(path, playwright.BrowserContextStorageStateOptions{
		IndexedDB:   playwright.Bool(true),
		Credentials: playwright.Bool(true),
	}))
	testx.Equal(t, playwright.Bool(true), ctx.options.IndexedDB)
	testx.Equal(t, playwright.Bool(true), ctx.options.Credentials)
	testx.NotEqual(t, path, ctx.path)
	testx.Equal(t, directory, filepath.Dir(ctx.path))
	data, err := os.ReadFile(path)
	testx.NoError(t, err)
	testx.Equal(t, ctx.data, data)
	for _, entry := range []struct {
		path string
		mode os.FileMode
	}{{path, 0o600}, {directory, 0o700}} {
		info, err := os.Stat(entry.path)
		testx.NoError(t, err)
		testx.Equal(t, entry.mode, info.Mode().Perm())
	}
	entries, err := os.ReadDir(directory)
	testx.NoError(t, err)
	testx.Len(t, entries, 1)
	testx.Equal(t, filepath.Base(path), entries[0].Name())
}

func TestSaveStateFailurePreservesExistingFile(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "partial write", err: errx.New("fixture storage failure")},
		{name: "invalid output"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "state.json")
			previous := []byte(`{"cookies":[],"origins":[]}`)
			testx.NoError(t, os.WriteFile(path, previous, 0o600))
			ctx := &storageContext{data: []byte(`{"cookies":`), err: tc.err}
			session := &Session{ctx: ctx}
			testx.ErrorIs(t, session.SaveState(path), ErrStateSave)
			testx.NotEmpty(t, ctx.path)
			testx.NotEqual(t, path, ctx.path)
			testx.Equal(t, directory, filepath.Dir(ctx.path))
			current, err := os.ReadFile(path)
			testx.NoError(t, err)
			testx.Equal(t, previous, current)
			info, err := os.Stat(path)
			testx.NoError(t, err)
			testx.Equal(t, os.FileMode(0o600), info.Mode().Perm())
			_, err = os.Stat(ctx.path)
			testx.ErrorIs(t, err, os.ErrNotExist)
			temporary, err := filepath.Glob(filepath.Join(directory, ".camoufox-state-*.json"))
			testx.NoError(t, err)
			testx.Empty(t, temporary)
		})
	}
}

type storageContext struct {
	playwright.BrowserContext
	path    string
	data    []byte
	options playwright.BrowserContextStorageStateOptions
	err     error
}

func (*storageContext) IsClosed() bool { return false }

func (c *storageContext) StorageState(options ...playwright.BrowserContextStorageStateOptions) (*playwright.StorageState, error) {
	if len(options) != 1 || options[0].Path == nil {
		return nil, errx.New("fixture storage path is required")
	}
	c.options = options[0]
	c.path = *options[0].Path
	if err := os.WriteFile(c.path, c.data, 0o600); err != nil {
		return nil, err
	}
	return nil, c.err
}
