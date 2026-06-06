package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"testing"
	"time"
)

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// newTestServer builds an lspServer over caller-controlled pipes, bypassing the
// real process spawn and handshake, and starts its reader goroutine.
func newTestServer(stdout io.Reader, stdin io.Writer) *lspServer {
	s := &lspServer{
		cfg:         LspServerConfig{ServerID: "test", LanguageID: "go"},
		stdin:       nopWriteCloser{stdin},
		stdout:      bufio.NewReader(stdout),
		waiters:     make(map[int]chan json.RawMessage),
		diagnostics: make(map[string]json.RawMessage),
		openDocs:    make(map[string]bool),
	}
	go s.readLoop()
	return s
}

func TestFramingRoundTripByteCount(t *testing.T) {
	var buf bytes.Buffer
	// A multibyte payload: byte length differs from rune count, so a
	// character-count header would corrupt the read.
	payload := map[string]string{"msg": "héllo 日本語"}
	if err := writeMessage(&buf, payload); err != nil {
		t.Fatal(err)
	}

	// The header must report the body's byte length.
	body, _ := json.Marshal(payload)
	want := []byte("Content-Length: ")
	if !bytes.HasPrefix(buf.Bytes(), want) {
		t.Fatal("missing Content-Length header")
	}

	got, err := readMessage(bufio.NewReader(&buf))
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("round-trip = %s, want %s", got, body)
	}
}

func TestDemuxByID(t *testing.T) {
	stdoutR, stdoutW := io.Pipe()
	s := newTestServer(stdoutR, io.Discard)
	defer s.failAll(io.EOF)

	results := make(chan string, 2)
	call := func() {
		raw, err := s.call("m", nil, 2*time.Second)
		if err != nil {
			results <- "ERR"
			return
		}
		var v string
		_ = json.Unmarshal(raw, &v)
		results <- v
	}
	// Start A (gets id 1), then B (gets id 2).
	go call()
	time.Sleep(50 * time.Millisecond)
	go call()
	time.Sleep(50 * time.Millisecond)

	// Respond out of order: id 2 first, then id 1.
	_ = writeMessage(stdoutW, map[string]interface{}{"jsonrpc": "2.0", "id": 2, "result": "two"})
	_ = writeMessage(stdoutW, map[string]interface{}{"jsonrpc": "2.0", "id": 1, "result": "one"})

	got := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case r := <-results:
			got[r] = true
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for demuxed responses")
		}
	}
	if !got["one"] || !got["two"] {
		t.Errorf("expected both responses routed by id, got %v", got)
	}
}

func TestDiagnosticsRouting(t *testing.T) {
	stdoutR, stdoutW := io.Pipe()
	s := newTestServer(stdoutR, io.Discard)
	defer s.failAll(io.EOF)

	uri := "file:///tmp/x.go"
	_ = writeMessage(stdoutW, map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "textDocument/publishDiagnostics",
		"params": map[string]interface{}{
			"uri":         uri,
			"diagnostics": []map[string]interface{}{{"message": "boom", "severity": 1}},
		},
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := s.cachedDiagnostics(uri); ok {
			return // cached by URI as expected
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("publishDiagnostics was not cached by URI")
}

func TestServerRequestAnsweredWithNull(t *testing.T) {
	stdoutR, stdoutW := io.Pipe()
	stdinR, stdinW := io.Pipe()
	s := newTestServer(stdoutR, stdinW)
	defer s.failAll(io.EOF)

	// The server sends a request; the reader goroutine must reply result:null
	// or the real server would deadlock (KTD2b).
	go func() {
		_ = writeMessage(stdoutW, map[string]interface{}{
			"jsonrpc": "2.0", "id": 99, "method": "window/workDoneProgress/create", "params": map[string]interface{}{},
		})
	}()

	reply, err := readMessage(bufio.NewReader(stdinR))
	if err != nil {
		t.Fatalf("expected a reply on stdin: %v", err)
	}
	var resp struct {
		ID     int             `json:"id"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(reply, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ID != 99 {
		t.Errorf("reply id = %d, want 99", resp.ID)
	}
	if string(resp.Result) != "null" {
		t.Errorf("reply result = %s, want null", resp.Result)
	}
}

func TestRegistrySelection(t *testing.T) {
	configs := resolveConfigs(nil)
	cases := map[string]string{
		"main.go":    "gopls",
		"app.tsx":    "typescript",
		"mod.mjs":    "typescript",
		"script.py":  "pyright",
		"index.html": "html",
	}
	for file, wantID := range cases {
		cfg, ok := configForFile(configs, file)
		if !ok {
			t.Errorf("%s: no config found", file)
			continue
		}
		if cfg.ServerID != wantID {
			t.Errorf("%s: server = %q, want %q", file, cfg.ServerID, wantID)
		}
	}
	if _, ok := configForFile(configs, "notes.unknownext"); ok {
		t.Error("unknown extension should resolve no config")
	}
}

func TestRegistryUserOverrideWins(t *testing.T) {
	override := LspServerConfig{
		ServerID:   "gopls",
		Command:    "my-gopls",
		Extensions: []string{"go"},
		LanguageID: "go",
	}
	configs := resolveConfigs([]LspServerConfig{override})
	cfg, ok := configForFile(configs, "main.go")
	if !ok || cfg.Command != "my-gopls" {
		t.Errorf("user override should win, got %+v (ok=%v)", cfg, ok)
	}
	// The built-in count must not grow — gopls is replaced, not duplicated.
	count := 0
	for _, c := range configs {
		if c.ServerID == "gopls" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected one gopls entry, got %d", count)
	}
}

func TestMissingBinaryReturnsInstallHint(t *testing.T) {
	pool := NewLspPool(t.TempDir(), []LspServerConfig{{
		ServerID:    "ghost",
		Command:     "orkestra-nonexistent-server-xyz",
		Extensions:  []string{"ghost"},
		LanguageID:  "ghost",
		InstallHint: "install the ghost server",
	}})
	_, err := pool.serverForFile("x.ghost")
	if err == nil {
		t.Fatal("expected an error for a missing server binary")
	}
	if err.Error() != "install the ghost server" {
		t.Errorf("error = %q, want the install hint", err.Error())
	}
}

func TestResolveFilePathRejectsTraversal(t *testing.T) {
	pool := NewLspPool(t.TempDir(), nil)
	if _, err := pool.resolveFilePath("../../etc/passwd"); err == nil {
		t.Error("expected traversal outside the workspace to be rejected")
	}
	if _, err := pool.resolveFilePath("sub/file.go"); err != nil {
		t.Errorf("a path inside the workspace should resolve, got %v", err)
	}
}
