package mcp

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/adryanev/orkestra/pkg/env"
)

// LspPool owns the language servers for a single workspace root. Servers are
// started lazily on first use, keyed by server id, reused across calls, and
// shut down together. It replaces the previous one-gopls-per-workspace manager.
type LspPool struct {
	root    string
	configs []LspServerConfig

	mu       sync.Mutex
	servers  map[string]*lspServer // serverID -> server
	starting map[string]*serverStart
}

type serverStart struct {
	done   chan struct{}
	server *lspServer
	err    error
}

// NewLspPool builds a pool for workspaceRoot. userOverrides (possibly nil)
// merge over the built-in language-server configs, user winning per server id.
func NewLspPool(workspaceRoot string, userOverrides []LspServerConfig) *LspPool {
	return &LspPool{
		root:     workspaceRoot,
		configs:  resolveConfigs(userOverrides),
		servers:  make(map[string]*lspServer),
		starting: make(map[string]*serverStart),
	}
}

// serverForFile selects the server config by the file's extension, validates
// the binary on the captured PATH (returning the install hint when missing),
// and returns a running server, starting one if needed.
func (p *LspPool) serverForFile(path string) (*lspServer, error) {
	cfg, ok := configForFile(p.configs, path)
	if !ok {
		ext := strings.TrimPrefix(filepath.Ext(path), ".")
		return nil, fmt.Errorf("no language server configured for .%s files", ext)
	}
	return p.getOrStart(cfg)
}

func (p *LspPool) getOrStart(cfg LspServerConfig) (*lspServer, error) {
	p.mu.Lock()

	if s, ok := p.servers[cfg.ServerID]; ok {
		if s.alive() {
			p.mu.Unlock()
			return s, nil
		}
		delete(p.servers, cfg.ServerID) // reap a dead server before restarting
	}
	if start, ok := p.starting[cfg.ServerID]; ok {
		p.mu.Unlock()
		<-start.done
		if start.err != nil {
			return nil, start.err
		}
		if start.server != nil && start.server.alive() {
			return start.server, nil
		}
		return p.getOrStart(cfg)
	}

	start := &serverStart{done: make(chan struct{})}
	p.starting[cfg.ServerID] = start
	p.mu.Unlock()

	// Validate the binary on the captured PATH so a server installed via
	// nvm/fnm/asdf resolves, returning the actionable install hint when absent
	// (R17b) instead of hanging or panicking.
	if env.LookPath(cfg.Command) == "" {
		start.err = fmt.Errorf("%s", cfg.InstallHint)
		p.finishStart(cfg.ServerID, start)
		return nil, start.err
	}

	s, err := startServer(p.root, cfg)
	start.server = s
	start.err = err
	if err != nil {
		p.finishStart(cfg.ServerID, start)
		return nil, err
	}
	p.mu.Lock()
	if existing, ok := p.servers[cfg.ServerID]; ok && existing.alive() {
		start.server = existing
		s.Close()
	} else {
		p.servers[cfg.ServerID] = s
	}
	delete(p.starting, cfg.ServerID)
	close(start.done)
	p.mu.Unlock()
	return start.server, nil
}

func (p *LspPool) finishStart(serverID string, start *serverStart) {
	p.mu.Lock()
	delete(p.starting, serverID)
	close(start.done)
	p.mu.Unlock()
}

// Shutdown stops every running server in the pool.
func (p *LspPool) Shutdown() {
	p.mu.Lock()
	servers := make([]*lspServer, 0, len(p.servers))
	for id, s := range p.servers {
		servers = append(servers, s)
		delete(p.servers, id)
	}
	p.mu.Unlock()
	for _, s := range servers {
		s.Close()
	}
}

// resolveFilePath turns a possibly-relative, worktree-relative file path into
// an absolute path and verifies it stays within the workspace root, rejecting
// path traversal (defense in depth alongside the MCP handler validation).
func (p *LspPool) resolveFilePath(file string) (string, error) {
	abs := file
	if !filepath.IsAbs(file) {
		abs = filepath.Join(p.root, file)
	}
	abs = filepath.Clean(abs)
	rootClean := filepath.Clean(p.root)
	if abs != rootClean && !strings.HasPrefix(abs, rootClean+string(filepath.Separator)) {
		return "", fmt.Errorf("file %q is outside the workspace", file)
	}
	return abs, nil
}
