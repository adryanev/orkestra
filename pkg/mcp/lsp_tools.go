package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// LSP wire types (subset). Positions are 0-based on the wire; the tool boundary
// is 1-based.
type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

type lspLocation struct {
	URI   string   `json:"uri"`
	Range lspRange `json:"range"`
}

// positionParams builds the textDocument/position params with the 1-based tool
// inputs converted to 0-based wire positions.
func positionParams(uri string, line, character int) map[string]interface{} {
	return map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": uri},
		"position":     map[string]interface{}{"line": line - 1, "character": character - 1},
	}
}

// withOpenDoc resolves the file, gets its server, opens the document, runs fn,
// then closes the document. It centralizes the open/close lifecycle.
func (p *LspPool) withOpenDoc(file string, fn func(s *lspServer, uri string) (string, error)) (string, error) {
	abs, err := p.resolveFilePath(file)
	if err != nil {
		return "", err
	}
	s, err := p.serverForFile(abs)
	if err != nil {
		return "", err
	}
	uri, err := s.ensureOpen(abs)
	if err != nil {
		return "", err
	}
	defer s.closeDoc(uri)
	return fn(s, uri)
}

// GotoDefinition returns the definition location(s) for the symbol at the
// 1-based line/character in file.
func (p *LspPool) GotoDefinition(file string, line, character int) (string, error) {
	return p.withOpenDoc(file, func(s *lspServer, uri string) (string, error) {
		raw, err := s.call("textDocument/definition", positionParams(uri, line, character), defaultRequestTimeout)
		if err != nil {
			return "", err
		}
		locs := parseLocations(raw)
		if len(locs) == 0 {
			return "No definition found.", nil
		}
		return p.formatLocations("Definition", locs), nil
	})
}

// FindReferences returns reference locations for the symbol at the position.
func (p *LspPool) FindReferences(file string, line, character int) (string, error) {
	return p.withOpenDoc(file, func(s *lspServer, uri string) (string, error) {
		params := positionParams(uri, line, character)
		params["context"] = map[string]interface{}{"includeDeclaration": true}
		raw, err := s.call("textDocument/references", params, defaultRequestTimeout)
		if err != nil {
			return "", err
		}
		locs := parseLocations(raw)
		if len(locs) == 0 {
			return "No references found.", nil
		}
		return p.formatLocations("References", locs), nil
	})
}

// Hover returns the hover text for the symbol at the position.
func (p *LspPool) Hover(file string, line, character int) (string, error) {
	return p.withOpenDoc(file, func(s *lspServer, uri string) (string, error) {
		raw, err := s.call("textDocument/hover", positionParams(uri, line, character), defaultRequestTimeout)
		if err != nil {
			return "", err
		}
		text := parseHover(raw)
		if text == "" {
			return "No hover information.", nil
		}
		return text, nil
	})
}

// WorkspaceSymbols searches workspace symbols matching query. It does not open a
// document; it picks any configured server that is available (preferring one
// already running).
func (p *LspPool) WorkspaceSymbols(query string) (string, error) {
	s, err := p.anyServer()
	if err != nil {
		return "", err
	}
	raw, err := s.call("workspace/symbol", map[string]interface{}{"query": query}, defaultRequestTimeout)
	if err != nil {
		return "", err
	}
	var syms []struct {
		Name     string      `json:"name"`
		Kind     int         `json:"kind"`
		Location lspLocation `json:"location"`
	}
	if err := json.Unmarshal(raw, &syms); err != nil || len(syms) == 0 {
		return "No symbols found.", nil
	}
	var b strings.Builder
	lines := sourceLineCache{}
	fmt.Fprintf(&b, "Symbols matching %q:\n", query)
	for _, sym := range syms {
		fmt.Fprintf(&b, "  %s  %s\n", sym.Name, p.formatLocationLine(sym.Location, lines))
	}
	return b.String(), nil
}

// Diagnostics opens the document, waits a bounded interval for at least one
// publishDiagnostics for it, then returns the cached diagnostics.
func (p *LspPool) Diagnostics(file string) (string, error) {
	return p.withOpenDoc(file, func(s *lspServer, uri string) (string, error) {
		// Bounded poll of the cache rather than a fixed sleep: diagnostics are
		// pushed asynchronously after didOpen.
		deadline := time.Now().Add(3 * time.Second)
		var raw json.RawMessage
		var ok bool
		for time.Now().Before(deadline) {
			if raw, ok = s.cachedDiagnostics(uri); ok {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if !ok {
			return "No diagnostics.", nil
		}
		return p.formatDiagnostics(raw), nil
	})
}

// Rename computes a workspace-wide rename and applies the resulting edits to
// files on disk, returning a summary.
func (p *LspPool) Rename(file string, line, character int, newName string) (string, error) {
	return p.withOpenDoc(file, func(s *lspServer, uri string) (string, error) {
		params := positionParams(uri, line, character)
		params["newName"] = newName
		raw, err := s.call("textDocument/rename", params, defaultRequestTimeout)
		if err != nil {
			return "", err
		}
		applied, err := applyWorkspaceEdit(p.root, raw)
		if err != nil {
			return "", err
		}
		if applied == 0 {
			return "Rename produced no edits.", nil
		}
		return fmt.Sprintf("Renamed to %q across %d edit(s).", newName, applied), nil
	})
}

// anyServer returns a running server, preferring one already started, otherwise
// starting the first configured server whose binary is available.
func (p *LspPool) anyServer() (*lspServer, error) {
	p.mu.Lock()
	for _, s := range p.servers {
		if s.alive() {
			p.mu.Unlock()
			return s, nil
		}
	}
	p.mu.Unlock()
	var lastErr error
	for _, cfg := range p.configs {
		s, err := p.getOrStart(cfg)
		if err == nil {
			return s, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no language servers configured")
	}
	return nil, lastErr
}

// --- formatting ---

// parseLocations decodes a definition/references result, which may be a single
// Location or an array of Location.
func parseLocations(raw json.RawMessage) []lspLocation {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var arr []lspLocation
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr
	}
	var one lspLocation
	if err := json.Unmarshal(raw, &one); err == nil && one.URI != "" {
		return []lspLocation{one}
	}
	return nil
}

func (p *LspPool) formatLocations(label string, locs []lspLocation) string {
	var b strings.Builder
	lines := sourceLineCache{}
	fmt.Fprintf(&b, "%s (%d):\n", label, len(locs))
	for _, loc := range locs {
		fmt.Fprintf(&b, "  %s\n", p.formatLocationLine(loc, lines))
	}
	return b.String()
}

// formatLocationLine renders a location as "relpath:line:col  context", with
// 1-based line/column and the source line when readable.
func (p *LspPool) formatLocationLine(loc lspLocation, lines sourceLineCache) string {
	path := uriToPath(loc.URI)
	rel := path
	if r, err := filepath.Rel(p.root, path); err == nil {
		rel = r
	}
	line := loc.Range.Start.Line + 1
	col := loc.Range.Start.Character + 1
	ctx := lines.sourceLine(path, loc.Range.Start.Line)
	if ctx != "" {
		return fmt.Sprintf("%s:%d:%d  %s", rel, line, col, ctx)
	}
	return fmt.Sprintf("%s:%d:%d", rel, line, col)
}

type sourceLineCache map[string][]string

// sourceLine returns the trimmed source line (0-based index) from path, or "".
func (c sourceLineCache) sourceLine(path string, index int) string {
	lines, ok := c[path]
	if !ok {
		data, err := os.ReadFile(path)
		if err != nil {
			return ""
		}
		lines = strings.Split(string(data), "\n")
		c[path] = lines
	}
	if index < 0 || index >= len(lines) {
		return ""
	}
	return strings.TrimSpace(lines[index])
}

func parseHover(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var h struct {
		Contents json.RawMessage `json:"contents"`
	}
	if err := json.Unmarshal(raw, &h); err != nil {
		return ""
	}
	// contents may be MarkupContent {kind,value}, a string, or an array.
	var markup struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(h.Contents, &markup); err == nil && markup.Value != "" {
		return strings.TrimSpace(markup.Value)
	}
	var s string
	if err := json.Unmarshal(h.Contents, &s); err == nil && s != "" {
		return strings.TrimSpace(s)
	}
	var arr []struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(h.Contents, &arr); err == nil {
		var parts []string
		for _, a := range arr {
			if a.Value != "" {
				parts = append(parts, a.Value)
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	}
	return ""
}

var severityName = map[int]string{1: "error", 2: "warning", 3: "info", 4: "hint"}

func (p *LspPool) formatDiagnostics(raw json.RawMessage) string {
	var params struct {
		URI         string `json:"uri"`
		Diagnostics []struct {
			Range    lspRange `json:"range"`
			Severity int      `json:"severity"`
			Message  string   `json:"message"`
			Source   string   `json:"source"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return "No diagnostics."
	}
	if len(params.Diagnostics) == 0 {
		return "No diagnostics."
	}
	path := uriToPath(params.URI)
	rel := path
	if r, err := filepath.Rel(p.root, path); err == nil {
		rel = r
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Diagnostics for %s (%d):\n", rel, len(params.Diagnostics))
	for _, d := range params.Diagnostics {
		sev := severityName[d.Severity]
		if sev == "" {
			sev = "diagnostic"
		}
		fmt.Fprintf(&b, "  %s %d:%d %s\n", sev, d.Range.Start.Line+1, d.Range.Start.Character+1, strings.TrimSpace(d.Message))
	}
	return b.String()
}

// applyWorkspaceEdit applies a WorkspaceEdit (either `changes` or
// `documentChanges`) to files on disk, applying text edits per file bottom-up
// so earlier offsets stay valid. It returns the number of edits applied.
func applyWorkspaceEdit(root string, raw json.RawMessage) (int, error) {
	var edit struct {
		Changes         map[string][]textEdit `json:"changes"`
		DocumentChanges []struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			Edits []textEdit `json:"edits"`
		} `json:"documentChanges"`
	}
	if err := json.Unmarshal(raw, &edit); err != nil {
		return 0, fmt.Errorf("invalid workspace edit: %w", err)
	}

	perFile := map[string][]textEdit{}
	for uri, edits := range edit.Changes {
		path, err := workspaceEditPath(root, uri)
		if err != nil {
			return 0, err
		}
		perFile[path] = append(perFile[path], edits...)
	}
	for _, dc := range edit.DocumentChanges {
		path, err := workspaceEditPath(root, dc.TextDocument.URI)
		if err != nil {
			return 0, err
		}
		perFile[path] = append(perFile[path], dc.Edits...)
	}

	applied := 0
	for path, edits := range perFile {
		n, err := applyTextEdits(path, edits)
		if err != nil {
			return applied, err
		}
		applied += n
	}
	return applied, nil
}

func workspaceEditPath(root, uri string) (string, error) {
	path := filepath.Clean(uriToPath(uri))
	root = filepath.Clean(root)
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("workspace edit target %q is outside the workspace", uri)
	}
	return path, nil
}

type textEdit struct {
	Range   lspRange `json:"range"`
	NewText string   `json:"newText"`
}

// applyTextEdits rewrites path with edits applied. Edits are sorted to apply
// from the end of the file backward so earlier edits do not shift later ranges.
func applyTextEdits(path string, edits []textEdit) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	lines := strings.Split(string(data), "\n")

	for _, e := range edits {
		if e.Range.Start.Line < 0 || e.Range.Start.Line >= len(lines) {
			return 0, fmt.Errorf("edit start line %d is outside %s", e.Range.Start.Line, path)
		}
		if e.Range.End.Line != e.Range.Start.Line {
			return 0, fmt.Errorf("multi-line edit in %s is not supported", path)
		}
		if e.Range.Start.Character < 0 || e.Range.End.Character < e.Range.Start.Character {
			return 0, fmt.Errorf("invalid edit range in %s", path)
		}
	}

	sort.SliceStable(edits, func(i, j int) bool {
		a, b := edits[i].Range.Start, edits[j].Range.Start
		if a.Line != b.Line {
			return a.Line > b.Line // later lines first
		}
		return a.Character > b.Character // later columns first
	})

	for _, e := range edits {
		line := lines[e.Range.Start.Line]
		start := utf16ColumnByteOffset(line, e.Range.Start.Character)
		end := utf16ColumnByteOffset(line, e.Range.End.Character)
		if end < start {
			end = start
		}
		lines[e.Range.Start.Line] = line[:start] + e.NewText + line[end:]
	}

	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0644); err != nil {
		return 0, err
	}
	return len(edits), nil
}

func utf16ColumnByteOffset(s string, character int) int {
	if character <= 0 {
		return 0
	}
	units := 0
	for i, r := range s {
		width := 1
		if r > 0xFFFF {
			width = 2
		}
		if units+width > character {
			return i
		}
		units += width
		if units == character {
			return i + len(string(r))
		}
	}
	return len(s)
}
