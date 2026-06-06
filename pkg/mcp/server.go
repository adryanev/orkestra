// Package mcp implements orkestra's MCP server (JSON-RPC 2.0 over stdio, via the
// official Go SDK) and the language-server tools the agent calls back into.
//
// Stdout discipline (KTD8): the SDK owns stdout for the JSON-RPC stream. Every
// log or diagnostic in this package goes to stderr or a file; a stray write to
// stdout corrupts the protocol.
package mcp

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/adryanev/orkestra/pkg/workspace"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	serverName    = "orkestra"
	serverVersion = "0.1.0"
	gitCmdTimeout = 10 * time.Second
)

// Server is the orkestra MCP server, fixed to a single workspace for the
// session (as Korlap fixes KORLAP_WORKSPACE_ID). LSP tool arguments carry file
// paths relative to that workspace's worktree.
type Server struct {
	wm          *workspace.Manager
	workspaceID string
	root        string
	pool        *LspPool
}

// NewServer builds an MCP server bound to workspaceID. The workspace must exist
// because every tool resolves against its worktree.
func NewServer(wm *workspace.Manager, workspaceID string, lspOverrides []LspServerConfig) (*Server, error) {
	ws, err := wm.GetWorkspace(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("mcp server requires a valid --workspace: %w", err)
	}
	return &Server{
		wm:          wm,
		workspaceID: workspaceID,
		root:        ws.WorktreePath,
		pool:        NewLspPool(ws.WorktreePath, lspOverrides),
	}, nil
}

// buildServer constructs the SDK server with all tools registered.
func (s *Server) buildServer() *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: serverName, Version: serverVersion}, nil)
	s.register(srv)
	return srv
}

// Run registers the tools and serves JSON-RPC over stdio until the context is
// cancelled or stdin closes.
func (s *Server) Run(ctx context.Context) error {
	defer s.pool.Shutdown()
	return s.buildServer().Run(ctx, &mcp.StdioTransport{})
}

// --- tool input/output types (the SDK derives JSON Schemas from these) ---

type workspaceIDInput struct {
	ID string `json:"id" jsonschema:"registered workspace id"`
}

type workspaceInfoOutput struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	RepoPath     string `json:"repo_path"`
	WorktreePath string `json:"worktree_path"`
	Branch       string `json:"branch"`
	Status       string `json:"status"`
}

type renameBranchInput struct {
	ID        string `json:"id" jsonschema:"registered workspace id"`
	NewBranch string `json:"new_branch" jsonschema:"new git branch name"`
}

type notifyInput struct {
	ID      string `json:"id" jsonschema:"registered workspace id"`
	Message string `json:"message" jsonschema:"message to append to the workspace log"`
}

type messageOutput struct {
	Message string `json:"message"`
}

type positionInput struct {
	FilePath  string `json:"file_path" jsonschema:"path relative to the workspace worktree"`
	Line      int    `json:"line" jsonschema:"1-based line number"`
	Character int    `json:"character" jsonschema:"1-based column number"`
}

type fileInput struct {
	FilePath string `json:"file_path" jsonschema:"path relative to the workspace worktree"`
}

type symbolInput struct {
	Query string `json:"query" jsonschema:"workspace symbol query"`
}

type renameInput struct {
	FilePath  string `json:"file_path" jsonschema:"path relative to the workspace worktree"`
	Line      int    `json:"line" jsonschema:"1-based line number"`
	Character int    `json:"character" jsonschema:"1-based column number"`
	NewName   string `json:"new_name" jsonschema:"new symbol name"`
}

type textOutput struct {
	Text string `json:"text"`
}

// register installs every tool. Tools use typed structs so the SDK validates
// required fields before the handler runs.
func (s *Server) register(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{Name: "get_workspace_info", Description: "Return path, branch, and status for a workspace."}, s.getWorkspaceInfo)
	mcp.AddTool(srv, &mcp.Tool{Name: "rename_branch", Description: "Rename the git branch of a workspace."}, s.renameBranch)
	mcp.AddTool(srv, &mcp.Tool{Name: "notify", Description: "Append a message to the workspace log file."}, s.notify)

	mcp.AddTool(srv, &mcp.Tool{Name: "lsp_goto_definition", Description: "Go to the definition of the symbol at a position."}, s.lspGotoDefinition)
	mcp.AddTool(srv, &mcp.Tool{Name: "lsp_find_references", Description: "Find references to the symbol at a position."}, s.lspFindReferences)
	mcp.AddTool(srv, &mcp.Tool{Name: "lsp_references", Description: "Find references to the symbol at a position."}, s.lspFindReferences)
	mcp.AddTool(srv, &mcp.Tool{Name: "lsp_hover", Description: "Show hover information for the symbol at a position."}, s.lspHover)
	mcp.AddTool(srv, &mcp.Tool{Name: "lsp_workspace_symbols", Description: "Search workspace symbols by query."}, s.lspWorkspaceSymbols)
	mcp.AddTool(srv, &mcp.Tool{Name: "lsp_diagnostics", Description: "Return diagnostics for a file."}, s.lspDiagnostics)
	mcp.AddTool(srv, &mcp.Tool{Name: "lsp_rename", Description: "Rename a symbol across the workspace and apply the edits."}, s.lspRename)
}

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

// --- workspace tools ---

func (s *Server) requireBoundWorkspace(id string) error {
	if id != s.workspaceID {
		return fmt.Errorf("mcp server is bound to workspace %s, got %s", s.workspaceID, id)
	}
	return nil
}

func (s *Server) getWorkspaceInfo(ctx context.Context, _ *mcp.CallToolRequest, in workspaceIDInput) (*mcp.CallToolResult, workspaceInfoOutput, error) {
	if err := s.requireBoundWorkspace(in.ID); err != nil {
		return nil, workspaceInfoOutput{}, err
	}
	ws, err := s.wm.GetWorkspace(in.ID)
	if err != nil {
		return nil, workspaceInfoOutput{}, fmt.Errorf("failed to get workspace info: %w", err)
	}
	out := workspaceInfoOutput{
		ID: ws.ID, Name: ws.Name, RepoPath: ws.RepoPath,
		WorktreePath: ws.WorktreePath, Branch: ws.Branch, Status: ws.Status,
	}
	return text(fmt.Sprintf("%s (%s) on %s [%s] at %s", out.Name, out.ID, out.Branch, out.Status, out.WorktreePath)), out, nil
}

func (s *Server) renameBranch(ctx context.Context, _ *mcp.CallToolRequest, in renameBranchInput) (*mcp.CallToolResult, messageOutput, error) {
	if err := s.requireBoundWorkspace(in.ID); err != nil {
		return nil, messageOutput{}, err
	}
	ws, err := s.wm.GetWorkspace(in.ID)
	if err != nil {
		return nil, messageOutput{}, fmt.Errorf("failed to get workspace: %w", err)
	}
	// Reject names git itself would reject before touching the repo (R20).
	if err := validBranchName(s.root, in.NewBranch); err != nil {
		return nil, messageOutput{}, err
	}

	rctx, cancel := context.WithTimeout(ctx, gitCmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(rctx, "git", "branch", "-m", in.NewBranch)
	cmd.Dir = ws.WorktreePath
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, messageOutput{}, fmt.Errorf("failed to rename branch: %w: %s", err, out)
	}
	if err := s.wm.UpdateWorkspaceBranch(in.ID, in.NewBranch); err != nil {
		return nil, messageOutput{}, fmt.Errorf("branch renamed but state update failed: %w", err)
	}

	msg := fmt.Sprintf("Branch for workspace %s renamed to %s", in.ID, in.NewBranch)
	return text(msg), messageOutput{Message: msg}, nil
}

func (s *Server) notify(ctx context.Context, _ *mcp.CallToolRequest, in notifyInput) (*mcp.CallToolResult, messageOutput, error) {
	if err := s.requireBoundWorkspace(in.ID); err != nil {
		return nil, messageOutput{}, err
	}
	path, err := notifyWorkspace(s.wm, in.ID, in.Message)
	if err != nil {
		return nil, messageOutput{}, err
	}
	msg := fmt.Sprintf("Notification logged to %s", path)
	return text(msg), messageOutput{Message: msg}, nil
}

// --- LSP tools ---

func (s *Server) lspGotoDefinition(ctx context.Context, _ *mcp.CallToolRequest, in positionInput) (*mcp.CallToolResult, textOutput, error) {
	return lspText(s.pool.GotoDefinition(in.FilePath, in.Line, in.Character))
}

func (s *Server) lspFindReferences(ctx context.Context, _ *mcp.CallToolRequest, in positionInput) (*mcp.CallToolResult, textOutput, error) {
	return lspText(s.pool.FindReferences(in.FilePath, in.Line, in.Character))
}

func (s *Server) lspHover(ctx context.Context, _ *mcp.CallToolRequest, in positionInput) (*mcp.CallToolResult, textOutput, error) {
	return lspText(s.pool.Hover(in.FilePath, in.Line, in.Character))
}

func (s *Server) lspWorkspaceSymbols(ctx context.Context, _ *mcp.CallToolRequest, in symbolInput) (*mcp.CallToolResult, textOutput, error) {
	return lspText(s.pool.WorkspaceSymbols(in.Query))
}

func (s *Server) lspDiagnostics(ctx context.Context, _ *mcp.CallToolRequest, in fileInput) (*mcp.CallToolResult, textOutput, error) {
	return lspText(s.pool.Diagnostics(in.FilePath))
}

func (s *Server) lspRename(ctx context.Context, _ *mcp.CallToolRequest, in renameInput) (*mcp.CallToolResult, textOutput, error) {
	return lspText(s.pool.Rename(in.FilePath, in.Line, in.Character, in.NewName))
}

// lspText adapts a (string, error) tool result to the SDK handler shape.
func lspText(out string, err error) (*mcp.CallToolResult, textOutput, error) {
	if err != nil {
		return nil, textOutput{}, err
	}
	return text(out), textOutput{Text: out}, nil
}

// validBranchName rejects names that git would reject, using
// `git check-ref-format --branch` (R20).
func validBranchName(dir, name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), gitCmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "check-ref-format", "--branch", name)
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("invalid branch name %q", name)
	}
	return nil
}
