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

	mu      sync.Mutex
	servers map[string]*lspServer // serverID -> server
}

// NewLspPool builds a pool for workspaceRoot. userOverrides (possibly nil)
// merge over the built-in language-server configs, user winning per server id.
func NewLspPool(workspaceRoot string, userOverrides []LspServerConfig) *LspPool {
	return &LspPool{
		root:    workspaceRoot,
		configs: resolveConfigs(userOverrides),
		servers: make(map[string]*lspServer),
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
	defer p.mu.Unlock()

	if s, ok := p.servers[cfg.ServerID]; ok {
		if s.alive() {
			return s, nil
		}
		delete(p.servers, cfg.ServerID) // reap a dead server before restarting
	}

	// Validate the binary on the captured PATH so a server installed via
	// nvm/fnm/asdf resolves, returning the actionable install hint when absent
	// (R17b) instead of hanging or panicking.
	if env.LookPath(cfg.Command) == "" {
		return nil, fmt.Errorf("%s", cfg.InstallHint)
	}

	s, err := startServer(p.root, cfg)
	if err != nil {
		return nil, err
	}
	p.servers[cfg.ServerID] = s
	return s, nil
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
