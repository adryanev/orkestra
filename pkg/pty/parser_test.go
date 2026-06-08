package pty

import (
	"testing"
)

// isPermissionPrompt is a test helper wrapping DetectPrompt for boolean checks.
func isPermissionPrompt(line string) bool {
	detected, _, _ := DetectPrompt(line)
	return detected
}

// extractCommand is a test helper that extracts the command from a prompt line.
func extractCommand(line string) string {
	_, _, command := DetectPrompt(line)
	return command
}

func TestDetectPrompt_ClaudeBashCommand(t *testing.T) {
	tests := []struct {
		name         string
		line         string
		wantDetected bool
		wantAgent    string
		wantCommand  string
	}{
		{
			name:         "Claude bash command with newline",
			line:         "Allow claude to execute this Bash command?\n  git status\n[Y/n]",
			wantDetected: true,
			wantAgent:    "claude",
			wantCommand:  "git status",
		},
		{
			name:         "Claude bash command inline",
			line:         "Allow claude to execute this Bash command? git status [Y/n]",
			wantDetected: true,
			wantAgent:    "claude",
			wantCommand:  "git status",
		},
		{
			name:         "Claude bash command with brackets",
			line:         "Allow claude to execute this Bash command? npm install [Y/n]",
			wantDetected: true,
			wantAgent:    "claude",
			wantCommand:  "npm install",
		},
		{
			name:         "Claude bash command case insensitive",
			line:         "Allow Claude to execute this Bash command? ls -la [Y/n]",
			wantDetected: true,
			wantAgent:    "claude",
			wantCommand:  "ls -la",
		},
		{
			name:         "Claude bash command with extra spaces",
			line:         "Allow  claude  to  execute  this  Bash  command?   docker ps [Y/n]",
			wantDetected: true,
			wantAgent:    "claude",
			wantCommand:  "docker ps",
		},
		{
			name:         "Claude bash command multiline format",
			line:         "Allow claude to execute this Bash command?\n  curl -s https://example.com\n[Y/n]",
			wantDetected: true,
			wantAgent:    "claude",
			wantCommand:  "curl -s https://example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detected, agent, command := DetectPrompt(tt.line)
			if detected != tt.wantDetected {
				t.Errorf("detected = %v, want %v", detected, tt.wantDetected)
			}
			if agent != tt.wantAgent {
				t.Errorf("agent = %q, want %q", agent, tt.wantAgent)
			}
			if command != tt.wantCommand {
				t.Errorf("command = %q, want %q", command, tt.wantCommand)
			}
		})
	}
}

func TestDetectPrompt_ClaudeMCPTool(t *testing.T) {
	tests := []struct {
		name         string
		line         string
		wantDetected bool
		wantAgent    string
		wantCommand  string
	}{
		{
			name:         "Claude MCP tool call",
			line:         "Allow claude to call mcp__orkestra__notify?",
			wantDetected: true,
			wantAgent:    "claude",
			wantCommand:  "mcp__orkestra__notify",
		},
		{
			name:         "Claude MCP tool call without question mark",
			line:         "Allow claude to call mcp__orkestra__lsp_diagnostics",
			wantDetected: true,
			wantAgent:    "claude",
			wantCommand:  "mcp__orkestra__lsp_diagnostics",
		},
		{
			name:         "Claude simple tool call",
			line:         "Allow claude to call WebSearch?",
			wantDetected: true,
			wantAgent:    "claude",
			wantCommand:  "WebSearch",
		},
		{
			name:         "Claude tool with underscores",
			line:         "Allow claude to call mcp__codebase_memory_mcp__search_code?",
			wantDetected: true,
			wantAgent:    "claude",
			wantCommand:  "mcp__codebase_memory_mcp__search_code",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detected, agent, command := DetectPrompt(tt.line)
			if detected != tt.wantDetected {
				t.Errorf("detected = %v, want %v", detected, tt.wantDetected)
			}
			if agent != tt.wantAgent {
				t.Errorf("agent = %q, want %q", agent, tt.wantAgent)
			}
			if command != tt.wantCommand {
				t.Errorf("command = %q, want %q", command, tt.wantCommand)
			}
		})
	}
}

func TestDetectPrompt_Codex(t *testing.T) {
	tests := []struct {
		name         string
		line         string
		wantDetected bool
		wantAgent    string
		wantCommand  string
	}{
		{
			name:         "Codex run command with Y/n",
			line:         "Run command: git status [Y/n]",
			wantDetected: true,
			wantAgent:    "codex",
			wantCommand:  "git status",
		},
		{
			name:         "Codex run command without Y/n not matched",
			line:         "Run command: git status",
			wantDetected: false,
		},
		{
			name:         "Codex run command case insensitive",
			line:         "run command: npm test [Y/n]",
			wantDetected: true,
			wantAgent:    "codex",
			wantCommand:  "npm test",
		},
		{
			name:         "Codex execute prompt with Y/n",
			line:         "Execute: make build [Y/n]",
			wantDetected: true,
			wantAgent:    "codex",
			wantCommand:  "make build",
		},
		{
			name:         "Codex execute without Y/n not matched",
			line:         "Execute: make build",
			wantDetected: false,
		},
		{
			name:         "Codex execute with brackets",
			line:         "Execute: docker-compose up [Y/n]",
			wantDetected: true,
			wantAgent:    "codex",
			wantCommand:  "docker-compose up",
		},
		{
			name:         "Codex execute case insensitive",
			line:         "EXECUTE: cargo run [Y/n]",
			wantDetected: true,
			wantAgent:    "codex",
			wantCommand:  "cargo run",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detected, agent, command := DetectPrompt(tt.line)
			if detected != tt.wantDetected {
				t.Errorf("detected = %v, want %v", detected, tt.wantDetected)
			}
			if agent != tt.wantAgent {
				t.Errorf("agent = %q, want %q", agent, tt.wantAgent)
			}
			if command != tt.wantCommand {
				t.Errorf("command = %q, want %q", command, tt.wantCommand)
			}
		})
	}
}

func TestDetectPrompt_NonPrompts(t *testing.T) {
	tests := []struct {
		name string
		line string
	}{
		{
			name: "Regular output",
			line: "This is just normal output",
		},
		{
			name: "Git status output",
			line: "On branch main",
		},
		{
			name: "Claude bash prompt without approval token",
			line: "Allow claude to execute this Bash command? git status",
		},
		{
			name: "Error message",
			line: "Error: command not found",
		},
		{
			name: "Empty line",
			line: "",
		},
		{
			name: "Whitespace only",
			line: "   \t  \n  ",
		},
		{
			name: "Similar but not prompt",
			line: "The claude agent will execute commands",
		},
		{
			name: "Partial match",
			line: "Allow claude to",
		},
		{
			name: "Another partial",
			line: "Run command",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detected, agent, command := DetectPrompt(tt.line)
			if detected {
				t.Errorf("detected = true for non-prompt line, got agent=%q command=%q", agent, command)
			}
			if agent != "" {
				t.Errorf("agent = %q, want empty string", agent)
			}
			if command != "" {
				t.Errorf("command = %q, want empty string", command)
			}
		})
	}
}

func TestIsPermissionPrompt(t *testing.T) {
	tests := []struct {
		name string
		line string
		want bool
	}{
		{
			name: "Claude prompt",
			line: "Allow claude to execute this Bash command? git status [Y/n]",
			want: true,
		},
		{
			name: "Codex prompt with Y/n",
			line: "Run command: npm test [Y/n]",
			want: true,
		},
		{
			name: "Codex prompt without Y/n not matched",
			line: "Run command: npm test",
			want: false,
		},
		{
			name: "Non-prompt",
			line: "Some random output",
			want: false,
		},
		{
			name: "Empty",
			line: "",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPermissionPrompt(tt.line); got != tt.want {
				t.Errorf("isPermissionPrompt() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtractCommand(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{
			name: "Claude bash command",
			line: "Allow claude to execute this Bash command? git status [Y/n]",
			want: "git status",
		},
		{
			name: "Codex run command with Y/n",
			line: "Run command: npm install [Y/n]",
			want: "npm install",
		},
		{
			name: "Claude MCP tool",
			line: "Allow claude to call mcp__orkestra__notify?",
			want: "mcp__orkestra__notify",
		},
		{
			name: "Non-prompt",
			line: "Some random text",
			want: "",
		},
		{
			name: "Empty",
			line: "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractCommand(tt.line); got != tt.want {
				t.Errorf("extractCommand() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDetectPrompt_ComplexCommands(t *testing.T) {
	tests := []struct {
		name         string
		line         string
		wantDetected bool
		wantAgent    string
		wantCommand  string
	}{
		{
			name:         "Command with pipes",
			line:         "Allow claude to execute this Bash command? cat file.txt | grep error [Y/n]",
			wantDetected: true,
			wantAgent:    "claude",
			wantCommand:  "cat file.txt | grep error",
		},
		{
			name:         "Command with redirects",
			line:         "Run command: echo 'test' > output.txt [Y/n]",
			wantDetected: true,
			wantAgent:    "codex",
			wantCommand:  "echo 'test' > output.txt",
		},
		{
			name:         "Command with quotes",
			line:         "Allow claude to execute this Bash command? git commit -m \"Initial commit\" [Y/n]",
			wantDetected: true,
			wantAgent:    "claude",
			wantCommand:  "git commit -m \"Initial commit\"",
		},
		{
			name:         "Multi-word command with flags",
			line:         "Execute: docker run -it --rm -v $(pwd):/app node:latest [Y/n]",
			wantDetected: true,
			wantAgent:    "codex",
			wantCommand:  "docker run -it --rm -v $(pwd):/app node:latest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detected, agent, command := DetectPrompt(tt.line)
			if detected != tt.wantDetected {
				t.Errorf("detected = %v, want %v", detected, tt.wantDetected)
			}
			if agent != tt.wantAgent {
				t.Errorf("agent = %q, want %q", agent, tt.wantAgent)
			}
			if command != tt.wantCommand {
				t.Errorf("command = %q, want %q", command, tt.wantCommand)
			}
		})
	}
}
