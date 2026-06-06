package mcp

import (
	"bufio"
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

// maxMessageBytes caps a single Content-Length framed message so a malformed or
// hostile header cannot drive an unbounded allocation and OOM the process.
const maxMessageBytes = 50 * 1024 * 1024 // 50 MiB

// lspServer is a running language-server process and the demultiplexing state
// for its stdio JSON-RPC channel. A reader goroutine drains stdout and routes
// each message to a waiting request, the diagnostics cache, or a null reply. A
// separate writer goroutine owns stdin and drains an in-memory outbound queue.
//
// All stdin writes go through the writer goroutine, and enqueueing onto the
// queue never blocks (it only appends to memory). This is the key invariant
// (KTD2a): the reader goroutine answers server-initiated requests by enqueueing
// a reply, so it can never block on a stdin write. A large didOpen that the
// server is slow to drain backs up the writer goroutine alone; the reader keeps
// draining stdout, the server keeps making progress, and there is no deadlock.
type lspServer struct {
	cfg  LspServerConfig
	root string

	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stdout   *bufio.Reader
	waitOnce sync.Once

	// Outbound write queue, drained by writeLoop. enqueue appends and signals;
	// it never blocks on the pipe, so the reader is never blocked by a write.
	// outClosed (guarded by outMu) tells writeLoop to stop; it is distinct from
	// `closed` so the writer never reads handleMu-guarded state.
	outMu     sync.Mutex
	outCond   *sync.Cond
	outBuf    [][]byte
	outClosed bool

	handleMu    sync.Mutex
	nextID      int
	waiters     map[int]chan json.RawMessage
	diagnostics map[string]json.RawMessage // uri -> publishDiagnostics params
	closed      bool
	exitErr     error

	// openMu serializes the didOpen/didClose lifecycle so a concurrent caller
	// cannot send a request before the document's didOpen is enqueued, and so
	// the refcount transitions (0->1 sends didOpen, 1->0 sends didClose) happen
	// exactly once.
	openMu    sync.Mutex
	openCount map[string]int // uri -> in-flight open refcount
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
		openCount:   make(map[string]int),
	}
	s.outCond = sync.NewCond(&s.outMu)
	go s.writeLoop()
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
// when the server dies instead of waiting out their timeouts. It also wakes the
// writer goroutine so it can exit.
func (s *lspServer) failAll(err error) {
	s.handleMu.Lock()
	s.closed = true
	s.exitErr = err
	for id, ch := range s.waiters {
		close(ch)
		delete(s.waiters, id)
	}
	s.handleMu.Unlock()

	s.outMu.Lock()
	s.outClosed = true
	s.outCond.Broadcast()
	s.outMu.Unlock()
}

// writeLoop is the sole owner of stdin. It drains the outbound queue, blocking
// only on the pipe write — never on the queue — so a slow server backs up the
// queue without ever blocking the reader or callers.
func (s *lspServer) writeLoop() {
	for {
		s.outMu.Lock()
		for len(s.outBuf) == 0 && !s.outClosed {
			s.outCond.Wait()
		}
		if len(s.outBuf) == 0 && s.outClosed {
			s.outMu.Unlock()
			return
		}
		frame := s.outBuf[0]
		s.outBuf = s.outBuf[1:]
		s.outMu.Unlock()

		if _, err := s.stdin.Write(frame); err != nil {
			s.failAll(err)
			return
		}
	}
}

// enqueue appends a pre-framed message to the outbound queue. It never blocks
// on the pipe, so it is safe to call from the reader goroutine (replyNull).
func (s *lspServer) enqueue(frame []byte) {
	s.outMu.Lock()
	s.outBuf = append(s.outBuf, frame)
	s.outCond.Signal()
	s.outMu.Unlock()
}

// send frames a payload and enqueues it. Marshalling errors are reported.
func (s *lspServer) send(payload interface{}) error {
	frame, err := frameMessage(payload)
	if err != nil {
		return err
	}
	s.enqueue(frame)
	return nil
}

// call sends a request and waits for its response under a timeout. It registers
// the waiter under handleMu, releases it, then enqueues the request and waits on
// the channel holding no lock (KTD2a).
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
	if err := s.send(req); err != nil {
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
		err := fmt.Errorf("lsp request %q timed out after %s", method, timeout)
		s.failAll(err)
		_ = s.stdin.Close()
		if s.cmd != nil && s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
			go s.waitProcess()
		}
		return nil, err
	}
}

func (s *lspServer) notify(method string, params interface{}) error {
	return s.send(map[string]interface{}{"jsonrpc": "2.0", "method": method, "params": params})
}

func (s *lspServer) replyNull(id json.RawMessage) {
	_ = s.send(map[string]interface{}{"jsonrpc": "2.0", "id": json.RawMessage(id), "result": nil})
}

// Close terminates the server process and unblocks any waiters. It sends the
// shutdown/exit handshake before marking the server closed so the request is
// actually transmitted.
func (s *lspServer) Close() {
	s.handleMu.Lock()
	already := s.closed
	s.handleMu.Unlock()
	if !already {
		// Best-effort graceful shutdown before killing. Sent while the server
		// is still open so call() does not short-circuit.
		_, _ = s.call("shutdown", nil, time.Second)
		_ = s.notify("exit", nil)
	}

	s.failAll(fmt.Errorf("server closed"))
	_ = s.stdin.Close()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	s.waitProcess()
}

func (s *lspServer) alive() bool {
	s.handleMu.Lock()
	defer s.handleMu.Unlock()
	return !s.closed
}

func (s *lspServer) waitProcess() {
	if s.cmd == nil {
		return
	}
	s.waitOnce.Do(func() { _ = s.cmd.Wait() })
}

// --- document lifecycle ---

// ensureOpen increments the open refcount for path and, on the 0->1 transition,
// reads the content and enqueues textDocument/didOpen. openMu is held across the
// enqueue so the didOpen is queued (FIFO) before any concurrent caller's request
// — the subsequent LSP request can never reach the server before the open.
func (s *lspServer) ensureOpen(path string) (string, error) {
	uri := pathToURI(path)
	s.openMu.Lock()
	defer s.openMu.Unlock()

	if s.openCount[uri] > 0 {
		s.openCount[uri]++
		return uri, nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read %s: %w", path, err)
	}
	if err := s.notify("textDocument/didOpen", map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri":        uri,
			"languageId": s.cfg.LanguageID,
			"version":    1,
			"text":       string(content),
		},
	}); err != nil {
		return "", err
	}
	s.openCount[uri] = 1
	return uri, nil
}

// closeDoc decrements the open refcount and, on the 1->0 transition, enqueues
// textDocument/didClose so the document is closed only after the last in-flight
// caller is done with it.
func (s *lspServer) closeDoc(uri string) {
	s.openMu.Lock()
	defer s.openMu.Unlock()

	count := s.openCount[uri]
	if count <= 0 {
		return
	}
	count--
	if count > 0 {
		s.openCount[uri] = count
		return
	}
	delete(s.openCount, uri)
	_ = s.notify("textDocument/didClose", map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": uri},
	})
}

// cachedDiagnostics returns the most recent publishDiagnostics params for uri.
func (s *lspServer) cachedDiagnostics(uri string) (json.RawMessage, bool) {
	s.handleMu.Lock()
	defer s.handleMu.Unlock()
	d, ok := s.diagnostics[uri]
	return d, ok
}

// --- base protocol framing ---

// frameMessage serializes a JSON-RPC payload into a Content-Length framed
// byte slice. The length is the byte count of the body, not its character count.
func frameMessage(payload interface{}) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	return append([]byte(header), body...), nil
}

// writeMessage frames a payload and writes it to w in one call.
func writeMessage(w io.Writer, payload interface{}) error {
	frame, err := frameMessage(payload)
	if err != nil {
		return err
	}
	_, err = w.Write(frame)
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
	if contentLength > maxMessageBytes {
		return nil, fmt.Errorf("Content-Length %d exceeds maximum allowed %d bytes", contentLength, maxMessageBytes)
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
