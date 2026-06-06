package mcp

import (
	"path/filepath"
	"strings"
)

// LspServerConfig describes how to launch and address a language server. The
// set is config-driven so a deployment can override the command for a language
// (a pinned gopls, a project-local typescript-language-server) without code
// changes.
type LspServerConfig struct {
	// ServerID is the stable identity used as the pool key and the merge key
	// for user overrides.
	ServerID string `json:"server_id" mapstructure:"server_id"`
	// Command is the server binary name, resolved against the captured PATH.
	Command string `json:"command" mapstructure:"command"`
	// Args are passed to the server (commonly "--stdio").
	Args []string `json:"args" mapstructure:"args"`
	// Extensions are file extensions (without the leading dot) this server
	// handles.
	Extensions []string `json:"extensions" mapstructure:"extensions"`
	// LanguageID is the LSP languageId sent in textDocument/didOpen.
	LanguageID string `json:"language_id" mapstructure:"language_id"`
	// InstallHint is returned to the caller when Command is not on PATH.
	InstallHint string `json:"install_hint" mapstructure:"install_hint"`
}

// builtinConfigs returns the language servers orkestra supports out of the box:
// Go, TypeScript/JavaScript/Node, Python, and HTML.
func builtinConfigs() []LspServerConfig {
	return []LspServerConfig{
		{
			ServerID:    "gopls",
			Command:     "gopls",
			Args:        nil,
			Extensions:  []string{"go"},
			LanguageID:  "go",
			InstallHint: "gopls not found; install with: go install golang.org/x/tools/gopls@latest",
		},
		{
			ServerID:    "typescript",
			Command:     "typescript-language-server",
			Args:        []string{"--stdio"},
			Extensions:  []string{"ts", "tsx", "js", "jsx", "mjs", "cjs", "mts", "cts"},
			LanguageID:  "typescript",
			InstallHint: "typescript-language-server not found; install with: npm i -g typescript-language-server typescript",
		},
		{
			ServerID:    "pyright",
			Command:     "pyright-langserver",
			Args:        []string{"--stdio"},
			Extensions:  []string{"py", "pyi"},
			LanguageID:  "python",
			InstallHint: "pyright-langserver not found; install with: npm i -g pyright",
		},
		{
			ServerID:    "html",
			Command:     "vscode-html-language-server",
			Args:        []string{"--stdio"},
			Extensions:  []string{"html", "htm"},
			LanguageID:  "html",
			InstallHint: "vscode-html-language-server not found; install with: npm i -g vscode-langservers-extracted",
		},
	}
}

// resolveConfigs merges user overrides over the built-ins. A user entry whose
// ServerID matches a built-in replaces it; new ServerIDs are added. User
// entries are placed first so they win extension lookups on overlap.
func resolveConfigs(userOverrides []LspServerConfig) []LspServerConfig {
	overridden := make(map[string]bool, len(userOverrides))
	out := make([]LspServerConfig, 0, len(userOverrides)+len(builtinConfigs()))
	for _, c := range userOverrides {
		out = append(out, c)
		overridden[c.ServerID] = true
	}
	for _, c := range builtinConfigs() {
		if !overridden[c.ServerID] {
			out = append(out, c)
		}
	}
	return out
}

// configForExtension returns the first config that handles ext (given without a
// leading dot, case-insensitive). User overrides, placed first by
// resolveConfigs, win on overlap.
func configForExtension(configs []LspServerConfig, ext string) (LspServerConfig, bool) {
	ext = strings.ToLower(strings.TrimPrefix(ext, "."))
	for _, c := range configs {
		for _, e := range c.Extensions {
			if strings.ToLower(e) == ext {
				return c, true
			}
		}
	}
	return LspServerConfig{}, false
}

// configForFile selects the server config for a file path by its extension.
func configForFile(configs []LspServerConfig, path string) (LspServerConfig, bool) {
	return configForExtension(configs, filepath.Ext(path))
}
