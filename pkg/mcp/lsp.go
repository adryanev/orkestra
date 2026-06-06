package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/adryanev/orkestra/pkg/env"
)

// defaultRequestTimeout bounds a single LSP request so a wedged server cannot
// hang a tool call (R11).
const defaultRequestTimeout = 20 * time.Second

// lspServer is a running language-server process and the demultiplexing state
// for its stdio JSON-RPC channel. One reader goroutine drains stdout and routes
// each message to a waiting request, the diagnostics cache, or a null reply.
//
// Two independent locks (KTD2a): writeMu guards stdin writes; handleMu guards
// the request bookkeeping (id counter, waiters, diagnostics, open documents). A
// request registers its waiter under handleMu, releases it, then writes under
// writeMu and blocks on its channel holding no lock — so a large didOpen write
// can never deadlock against the reader goroutine draining stdout (a hazard
// Korlap documents for pyright).
type lspServer struct {
	cfg  LspServerConfig
	root string

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader

	writeMu sync.Mutex

	handleMu    sync.Mutex
	nextID      int
	waiters     map[int]chan json.RawMessage
	diagnostics map[string]json.RawMessage // uri -> publishDiagnostics params
	openDocs    map[string]bool            // uri -> open
	closed      bool
	exitErr     error
}

// startServer spawns the language server, performs the initialize/initialized
// handshake, and runs any server-specific post-initialization (pyright's
// openFilesOnly + venv). The binary must already be validated on PATH.
func startServer(workspaceRoot string, cfg LspServerConfig) (*lspServer, error) {
	binPath := env.LookPath(cfg.Command)
	if binPath == "" {
		binPath = cfg.Command
	}

	cmd := exec.Command(binPath, cfg.Args...)
	cmd.Dir = workspaceRoot
	cmd.Env = serverEnv(workspaceRoot, cfg)
	// stderr is the server's diagnostic log channel; forward it to orkestra's
	// stderr so it never contaminates a protocol stream (KTD8).
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("lsp stdin pipe: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("lsp stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start %s: %w", cfg.Command, err)
	}

	s := &lspServer{
		cfg:         cfg,
		root:        workspaceRoot,
		cmd:         cmd,
		stdin:       stdin,
		stdout:      bufio.NewReader(stdoutPipe),
		waiters:     make(map[int]chan json.RawMessage),
		diagnostics: make(map[string]json.RawMessage),
		openDocs:    make(map[string]bool),
	}
	go s.readLoop()

	if err := s.handshake(); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// serverEnv builds the language server environment with terminal parity, plus
// pyright's virtualenv wiring when a .venv/venv with pyvenv.cfg exists in the
// worktree (so pyright resolves third-party imports).
func serverEnv(workspaceRoot string, cfg LspServerConfig) []string {
	environ := env.Environ()
	if cfg.ServerID != "pyright" {
		return environ
	}
	for _, name := range []string{".venv", "venv"} {
		venv := filepath.Join(workspaceRoot, name)
		if _, err := os.Stat(filepath.Join(venv, "pyvenv.cfg")); err != nil {
			continue
		}
		bin := filepath.Join(venv, "bin")
		out := make([]string, 0, len(environ)+1)
		for _, kv := range environ {
			if strings.HasPrefix(kv, "PATH=") {
				kv = "PATH=" + bin + string(os.PathListSeparator) + kv[len("PATH="):]
			}
			out = append(out, kv)
		}
		out = append(out, "VIRTUAL_ENV="+venv)
		return out
	}
	return environ
}

// handshake runs initialize -> response -> initialized, then server-specific
// configuration.
func (s *lspServer) handshake() error {
	initParams := map[string]interface{}{
		"processId":    os.Getpid(),
		"rootUri":      pathToURI(s.root),
		"capabilities": map[string]interface{}{},
		"workspaceFolders": []map[string]interface{}{
			{"uri": pathToURI(s.root), "name": filepath.Base(s.root)},
		},
	}
	if _, err := s.call("initialize", initParams, defaultRequestTimeout); err != nil {
		return fmt.Errorf("initialize failed: %w", err)
	}
	if err := s.notify("initialized", map[string]interface{}{}); err != nil {
		return fmt.Errorf("initialized notification failed: %w", err)
	}

	// pyright crawls the entire project by default, blocking every request for
	// minutes; openFilesOnly restricts analysis to opened documents.
	if s.cfg.ServerID == "pyright" {
		_ = s.notify("workspace/didChangeConfiguration", map[string]interface{}{
			"settings": map[string]interface{}{
				"python": map[string]interface{}{
					"analysis": map[string]interface{}{"diagnosticMode": "openFilesOnly"},
				},
			},
		})
	}
	return nil
}

// readLoop drains stdout, handling the three LSP message shapes (KTD2b):
// responses route to their waiter; server-initiated requests are answered with
// result:null so the server does not deadlock; notifications route
// publishDiagnostics to the per-URI cache and are otherwise ignored.
func (s *lspServer) readLoop() {
	for {
		raw, err := readMessage(s.stdout)
		if err != nil {
			s.failAll(err)
			return
		}
		var msg struct {
			ID     *json.RawMessage `json:"id"`
			Method string           `json:"method"`
			Params json.RawMessage  `json:"params"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}

		switch {
		case msg.ID != nil && msg.Method == "":
			// Response to one of our requests.
			if id, ok := decodeID(*msg.ID); ok {
				s.deliver(id, raw)
			}
		case msg.ID != nil && msg.Method != "":
			// Server-initiated request: reply result:null or it deadlocks.
			s.replyNull(*msg.ID)
		case msg.Method == "textDocument/publishDiagnostics":
			s.cacheDiagnostics(msg.Params)
		default:
			// Other notifications (logs, progress) are not actionable here.
		}
	}
}

func (s *lspServer) cacheDiagnostics(params json.RawMessage) {
	var p struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.URI == "" {
		return
	}
	s.handleMu.Lock()
	s.diagnostics[p.URI] = params
	s.handleMu.Unlock()
}

func (s *lspServer) deliver(id int, raw json.RawMessage) {
	s.handleMu.Lock()
	ch, ok := s.waiters[id]
	if ok {
		delete(s.waiters, id)
	}
	s.handleMu.Unlock()
	if ok {
		ch <- raw
	}
}

// failAll wakes every pending waiter with the reader error so callers unblock
// when the server dies instead of waiting out their timeouts.
func (s *lspServer) failAll(err error) {
	s.handleMu.Lock()
	s.closed = true
	s.exitErr = err
	for id, ch := range s.waiters {
		close(ch)
		delete(s.waiters, id)
	}
	s.handleMu.Unlock()
}

// call sends a request and waits for its response under a timeout, never
// holding handleMu and writeMu at once (KTD2a).
func (s *lspServer) call(method string, params interface{}, timeout time.Duration) (json.RawMessage, error) {
	s.handleMu.Lock()
	if s.closed {
		s.handleMu.Unlock()
		return nil, fmt.Errorf("lsp server %s is not running", s.cfg.ServerID)
	}
	s.nextID++
	id := s.nextID
	ch := make(chan json.RawMessage, 1)
	s.waiters[id] = ch
	s.handleMu.Unlock()

	req := map[string]interface{}{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	if err := s.write(req); err != nil {
		s.handleMu.Lock()
		delete(s.waiters, id)
		s.handleMu.Unlock()
		return nil, err
	}

	select {
	case raw, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("lsp server %s closed: %v", s.cfg.ServerID, s.exitErr)
		}
		return extractResult(raw)
	case <-time.After(timeout):
		s.handleMu.Lock()
		delete(s.waiters, id)
		s.handleMu.Unlock()
		return nil, fmt.Errorf("lsp request %q timed out after %s", method, timeout)
	}
}

func (s *lspServer) notify(method string, params interface{}) error {
	return s.write(map[string]interface{}{"jsonrpc": "2.0", "method": method, "params": params})
}

func (s *lspServer) replyNull(id json.RawMessage) {
	_ = s.write(map[string]interface{}{"jsonrpc": "2.0", "id": json.RawMessage(id), "result": nil})
}

// write serializes one message and writes it under writeMu only.
func (s *lspServer) write(payload interface{}) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return writeMessage(s.stdin, payload)
}

// Close terminates the server process and unblocks any waiters.
func (s *lspServer) Close() {
	s.handleMu.Lock()
	already := s.closed
	s.closed = true
	s.handleMu.Unlock()
	if !already {
		// Best-effort graceful shutdown before killing.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, _ = s.call("shutdown", nil, time.Second)
		_ = s.notify("exit", nil)
		cancel()
		_ = ctx.Err()
	}
	_ = s.stdin.Close()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	_ = s.cmd.Wait()
}

func (s *lspServer) alive() bool {
	s.handleMu.Lock()
	defer s.handleMu.Unlock()
	return !s.closed
}

// --- document lifecycle ---

// ensureOpen sends textDocument/didOpen for path if it is not already open,
// reading the content from disk and tagging it with the registry languageId.
func (s *lspServer) ensureOpen(path string) (string, error) {
	uri := pathToURI(path)
	s.handleMu.Lock()
	open := s.openDocs[uri]
	s.handleMu.Unlock()
	if open {
		return uri, nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read %s: %w", path, err)
	}
	err = s.notify("textDocument/didOpen", map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri":        uri,
			"languageId": s.cfg.LanguageID,
			"version":    1,
			"text":       string(content),
		},
	})
	if err != nil {
		return "", err
	}
	s.handleMu.Lock()
	s.openDocs[uri] = true
	s.handleMu.Unlock()
	return uri, nil
}

func (s *lspServer) closeDoc(uri string) {
	s.handleMu.Lock()
	open := s.openDocs[uri]
	if open {
		delete(s.openDocs, uri)
	}
	s.handleMu.Unlock()
	if open {
		_ = s.notify("textDocument/didClose", map[string]interface{}{
			"textDocument": map[string]interface{}{"uri": uri},
		})
	}
}

// cachedDiagnostics returns the most recent publishDiagnostics params for uri.
func (s *lspServer) cachedDiagnostics(uri string) (json.RawMessage, bool) {
	s.handleMu.Lock()
	defer s.handleMu.Unlock()
	d, ok := s.diagnostics[uri]
	return d, ok
}

// --- base protocol framing ---

// writeMessage frames a JSON-RPC payload with a Content-Length header. The
// length is the byte count of the body, not its character count.
func writeMessage(w io.Writer, payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	if _, err := io.WriteString(w, header); err != nil {
		return err
	}
	_, err = w.Write(body)
	return err
}

// readMessage reads one Content-Length framed message body from r.
func readMessage(r *bufio.Reader) (json.RawMessage, error) {
	contentLength := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break // end of headers
		}
		if name, value, ok := strings.Cut(line, ":"); ok {
			if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
				contentLength, err = strconv.Atoi(strings.TrimSpace(value))
				if err != nil {
					return nil, fmt.Errorf("invalid Content-Length: %w", err)
				}
			}
		}
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("missing Content-Length header")
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return json.RawMessage(body), nil
}

// extractResult returns the result field of a JSON-RPC response, or an error
// when the response carries an error object.
func extractResult(raw json.RawMessage) (json.RawMessage, error) {
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("lsp error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	return resp.Result, nil
}

// decodeID extracts a numeric JSON-RPC id. orkestra only issues numeric ids, so
// string ids (which only appear on server-initiated requests) are ignored here.
func decodeID(raw json.RawMessage) (int, bool) {
	var n int
	if err := json.Unmarshal(raw, &n); err == nil {
		return n, true
	}
	return 0, false
}

// pathToURI converts an absolute file path to a percent-encoded file:// URI.
func pathToURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	u := &url.URL{Scheme: "file", Path: abs}
	return u.String()
}

// uriToPath converts a file:// URI back to a filesystem path.
func uriToPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return uri
	}
	if u.Scheme != "file" {
		return uri
	}
	return u.Path
}
