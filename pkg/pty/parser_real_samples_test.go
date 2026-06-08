package pty

import (
	"testing"
)

// TestDetectPrompt_RealClaudeCodeSamples tests with actual Claude Code output
// captured from real sessions to ensure robust detection in production scenarios.
func TestDetectPrompt_RealClaudeCodeSamples(t *testing.T) {
	tests := []struct {
		name         string
		sample       string
		wantDetected bool
		wantAgent    string
		wantCommand  string
		description  string
	}{
		{
			name: "Claude Code bash permission - typical format",
			sample: `╭─ Tool Call ──────────────────────────────────────────────────────────╮
│ Bash                                                                 │
├──────────────────────────────────────────────────────────────────────┤
│ Command: git status                                                  │
│ Description: Check git status                                        │
╰──────────────────────────────────────────────────────────────────────╯

Allow claude to execute this Bash command?
  git status
[Y/n]`,
			wantDetected: true,
			wantAgent:    "claude",
			wantCommand:  "git status",
			description:  "Standard Claude Code bash permission prompt with tool call box",
		},
		{
			name: "Claude Code bash permission - single line",
			sample: `Allow claude to execute this Bash command? ls -la /tmp
[Y/n]`,
			wantDetected: true,
			wantAgent:    "claude",
			wantCommand:  "ls -la /tmp",
			description:  "Claude Code inline bash prompt without formatting",
		},
		{
			name: "Claude Code MCP tool - orkestra notify",
			sample: `╭─ Tool Call ──────────────────────────────────────────────────────────╮
│ mcp__orkestra__notify                                                │
├──────────────────────────────────────────────────────────────────────┤
│ {                                                                    │
│   "message": "Task completed",                                       │
│   "level": "info"                                                    │
│ }                                                                    │
╰──────────────────────────────────────────────────────────────────────╯

Allow claude to call mcp__orkestra__notify?
[Y/n]`,
			wantDetected: true,
			wantAgent:    "claude",
			wantCommand:  "mcp__orkestra__notify",
			description:  "Claude Code MCP tool permission with full tool call display",
		},
		{
			name: "Claude Code MCP tool - LSP diagnostics",
			sample: `Allow claude to call mcp__orkestra__lsp_diagnostics?
[Y/n]`,
			wantDetected: true,
			wantAgent:    "claude",
			wantCommand:  "mcp__orkestra__lsp_diagnostics",
			description:  "Claude Code MCP tool simple format",
		},
		{
			name: "Claude Code Read tool permission",
			sample: `╭─ Tool Call ──────────────────────────────────────────────────────────╮
│ Read                                                                 │
├──────────────────────────────────────────────────────────────────────┤
│ File: /home/user/project/main.go                                     │
╰──────────────────────────────────────────────────────────────────────╯

Allow claude to call Read?
[Y/n]`,
			wantDetected: true,
			wantAgent:    "claude",
			wantCommand:  "Read",
			description:  "Claude Code built-in Read tool",
		},
		{
			name: "Claude Code complex bash with pipes",
			sample: `Allow claude to execute this Bash command?
  cat package.json | grep version | awk '{print $2}'
[Y/n]`,
			wantDetected: true,
			wantAgent:    "claude",
			wantCommand:  "cat package.json | grep version | awk '{print $2}'",
			description:  "Complex bash command with pipes",
		},
		{
			name: "Claude Code docker compose command",
			sample: `Allow claude to execute this Bash command?
  docker-compose up -d --build
[Y/n]`,
			wantDetected: true,
			wantAgent:    "claude",
			wantCommand:  "docker-compose up -d --build",
			description:  "Docker compose command",
		},
		{
			name: "Claude Code git command with quotes",
			sample: `Allow claude to execute this Bash command?
  git commit -m "feat: add permission handling"
[Y/n]`,
			wantDetected: true,
			wantAgent:    "claude",
			wantCommand:  `git commit -m "feat: add permission handling"`,
			description:  "Git commit with quoted message",
		},
		{
			name:         "Claude Code multiline command with continuation",
			sample:       "Allow claude to execute this Bash command?\n  curl -X POST https://api.example.com/endpoint \\\n    -H \"Content-Type: application/json\" \\\n    -d '{\"key\": \"value\"}'\n[Y/n]",
			wantDetected: true,
			wantAgent:    "claude",
			wantCommand:  "curl -X POST https://api.example.com/endpoint \\\n    -H \"Content-Type: application/json\" \\\n    -d '{\"key\": \"value\"}'",
			description:  "Multiline curl command with backslash continuation",
		},
		{
			name: "Claude Code WebSearch MCP tool",
			sample: `Allow claude to call WebSearch?
[Y/n]`,
			wantDetected: true,
			wantAgent:    "claude",
			wantCommand:  "WebSearch",
			description:  "MCP WebSearch tool",
		},
		{
			name: "Claude Code nested MCP tool",
			sample: `Allow claude to call mcp__codebase_memory_mcp__search_code?
[Y/n]`,
			wantDetected: true,
			wantAgent:    "claude",
			wantCommand:  "mcp__codebase_memory_mcp__search_code",
			description:  "Deeply nested MCP tool name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test full sample - this is what matters in production
			// because we accumulate output in a buffer and detect on complete content
			detected, agent, command := DetectPrompt(tt.sample)
			if detected != tt.wantDetected {
				t.Errorf("detected = %v, want %v\nSample: %s\nDescription: %s",
					detected, tt.wantDetected, tt.sample, tt.description)
			}
			if agent != tt.wantAgent {
				t.Errorf("agent = %q, want %q\nDescription: %s",
					agent, tt.wantAgent, tt.description)
			}
			if command != tt.wantCommand {
				t.Errorf("command = %q, want %q\nDescription: %s",
					command, tt.wantCommand, tt.description)
			}

			// Note: We don't test line-by-line detection here because in a real
			// PTY streaming scenario, the daemon.go processes output and accumulates
			// it into a lineBuffer. Detection happens on the accumulated content,
			// not on individual incomplete lines. See daemon.go processWithPromptDetection()
		})
	}
}

// TestDetectPrompt_RealCodexSamples tests with actual Codex output patterns
func TestDetectPrompt_RealCodexSamples(t *testing.T) {
	tests := []struct {
		name         string
		sample       string
		wantDetected bool
		wantAgent    string
		wantCommand  string
		description  string
	}{
		{
			// Codex canonical single-line format: command and [Y/n] on the same line.
			// The box-with-separate-[Y/n] format is intentionally not detected because
			// the pattern requires [Y/n] inline to avoid false positives from log output.
			name:         "Codex run command - basic",
			sample:       "Run command: git status [Y/n]",
			wantDetected: true,
			wantAgent:    "codex",
			wantCommand:  "git status",
			description:  "Codex run command canonical single-line format",
		},
		{
			name: "Codex execute prompt",
			sample: `Execute: npm install
[Y/n]`,
			wantDetected: true,
			wantAgent:    "codex",
			wantCommand:  "npm install",
			description:  "Codex execute simple format",
		},
		{
			name: "Codex with complex docker command",
			sample: `Run command: docker run -it --rm -v $(pwd):/workspace -w /workspace golang:1.21 go test ./...
[Y/n]`,
			wantDetected: true,
			wantAgent:    "codex",
			wantCommand:  "docker run -it --rm -v $(pwd):/workspace -w /workspace golang:1.21 go test ./...",
			description:  "Codex complex docker test command",
		},
		{
			name: "Codex cargo build",
			sample: `Execute: cargo build --release
[Y/n]`,
			wantDetected: true,
			wantAgent:    "codex",
			wantCommand:  "cargo build --release",
			description:  "Codex Rust cargo command",
		},
		{
			name: "Codex make command",
			sample: `Run command: make clean && make build
[Y/n]`,
			wantDetected: true,
			wantAgent:    "codex",
			wantCommand:  "make clean && make build",
			description:  "Codex chained make commands",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detected, agent, command := DetectPrompt(tt.sample)
			if detected != tt.wantDetected {
				t.Errorf("detected = %v, want %v\nSample: %s\nDescription: %s",
					detected, tt.wantDetected, tt.sample, tt.description)
			}
			if agent != tt.wantAgent {
				t.Errorf("agent = %q, want %q\nDescription: %s",
					agent, tt.wantAgent, tt.description)
			}
			if command != tt.wantCommand {
				t.Errorf("command = %q, want %q\nDescription: %s",
					command, tt.wantCommand, tt.description)
			}
		})
	}
}

// TestDetectPrompt_EdgeCasesAndNoise tests with noisy output, ANSI codes, and edge cases
func TestDetectPrompt_EdgeCasesAndNoise(t *testing.T) {
	tests := []struct {
		name         string
		sample       string
		wantDetected bool
		wantAgent    string
		wantCommand  string
		description  string
	}{
		{
			name: "Mixed output with prompt embedded",
			sample: `Building project...
Done.
Allow claude to execute this Bash command?
  npm test
[Y/n]
Running tests...`,
			wantDetected: true,
			wantAgent:    "claude",
			wantCommand:  "npm test",
			description:  "Prompt embedded in regular output",
		},
		{
			name:         "Regular terminal output - no prompt",
			sample:       "On branch main\nYour branch is up to date with 'origin/main'.\n\nnothing to commit, working tree clean",
			wantDetected: false,
			description:  "Git status output without permission prompt",
		},
		{
			name:         "Error message - no prompt",
			sample:       "Error: command not found: git-status\nDid you mean 'git status'?",
			wantDetected: false,
			description:  "Error message that shouldn't trigger detection",
		},
		{
			name:         "Claude mentioned but not a prompt",
			sample:       "The Claude agent is analyzing your codebase...\nProcessing files: 42/100",
			wantDetected: false,
			description:  "Output mentioning Claude but not a permission request",
		},
		{
			name: "Prompt with ANSI color codes (simulated)",
			sample: `Allow claude to execute this Bash command?
  \033[1;32mgit status\033[0m
[Y/n]`,
			wantDetected: true,
			wantAgent:    "claude",
			wantCommand:  "\\033[1;32mgit status\\033[0m",
			description:  "Command with ANSI escape codes",
		},
		{
			name:         "Partial prompt - incomplete line",
			sample:       "Allow claude to execute this Bash",
			wantDetected: false,
			description:  "Incomplete prompt line",
		},
		{
			name:         "Similar wording but wrong structure",
			sample:       "claude will allow you to execute commands after approval",
			wantDetected: false,
			description:  "Similar words in wrong order",
		},
		{
			name: "Command with environment variables",
			sample: `Allow claude to execute this Bash command?
  export PATH=$PATH:/usr/local/bin && go build
[Y/n]`,
			wantDetected: true,
			wantAgent:    "claude",
			wantCommand:  "export PATH=$PATH:/usr/local/bin && go build",
			description:  "Command with environment variable syntax",
		},
		{
			name: "Command with heredoc syntax",
			sample: `Allow claude to execute this Bash command?
  cat <<EOF > config.yml
server:
  port: 8080
EOF
[Y/n]`,
			wantDetected: true,
			wantAgent:    "claude",
			wantCommand: `cat <<EOF > config.yml
server:
  port: 8080
EOF`,
			description: "Command with heredoc (multiline literal)",
		},
		{
			name:         "Empty or whitespace only",
			sample:       "   \t\n  \n\t  ",
			wantDetected: false,
			description:  "Whitespace only input",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detected, agent, command := DetectPrompt(tt.sample)
			if detected != tt.wantDetected {
				t.Errorf("detected = %v, want %v\nSample: %q\nDescription: %s",
					detected, tt.wantDetected, tt.sample, tt.description)
			}
			if tt.wantDetected {
				if agent != tt.wantAgent {
					t.Errorf("agent = %q, want %q\nDescription: %s",
						agent, tt.wantAgent, tt.description)
				}
				if command != tt.wantCommand {
					t.Errorf("command = %q, want %q\nDescription: %s",
						command, tt.wantCommand, tt.description)
				}
			}
		})
	}
}

// TestDetectPrompt_StreamingScenarios tests realistic streaming scenarios where
// prompts arrive character-by-character or in chunks
func TestDetectPrompt_StreamingScenarios(t *testing.T) {
	tests := []struct {
		name        string
		chunks      []string
		description string
	}{
		{
			name: "Character by character",
			chunks: []string{
				"A", "l", "l", "o", "w", " ", "c", "l", "a", "u", "d", "e",
				" ", "t", "o", " ", "e", "x", "e", "c", "u", "t", "e", " ",
				"t", "h", "i", "s", " ", "B", "a", "s", "h", " ", "c", "o",
				"m", "m", "a", "n", "d", "?", "\n", " ", " ", "d", "o", "c",
				"k", "e", "r", "-", "c", "o", "m", "p", "o", "s", "e", " ",
				"u", "p", " ", "-", "d", "\n", "[Y/n]",
			},
			description: "Simulates character-by-character streaming",
		},
		{
			name: "Word by word",
			chunks: []string{
				"Allow ", "claude ", "to ", "execute ", "this ", "Bash ",
				"command?\n", "  docker-compose ", "up ", "-d\n", "[Y/n]",
			},
			description: "Simulates word-by-word chunks",
		},
		{
			name: "Line by line",
			chunks: []string{
				"Allow claude to execute this Bash command?\n",
				"  docker-compose up -d\n",
				"[Y/n]",
			},
			description: "Simulates line-by-line streaming",
		},
		{
			name: "Irregular chunks",
			chunks: []string{
				"Allow claude to ex",
				"ecute this Bash command",
				"?\n  docker-co",
				"mpose up -d\n[Y/n]",
			},
			description: "Simulates irregular network packet boundaries",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Accumulate chunks and test detection at each step
			accumulated := ""
			detectedAtEnd := false

			for i, chunk := range tt.chunks {
				accumulated += chunk

				detected, agent, command := DetectPrompt(accumulated)
				if detected && !detectedAtEnd {
					detectedAtEnd = true
					t.Logf("Detected at chunk %d/%d: agent=%s command=%s",
						i+1, len(tt.chunks), agent, command)
				}
			}

			if !detectedAtEnd {
				t.Errorf("Failed to detect prompt after all chunks\nDescription: %s\nFinal accumulated: %q",
					tt.description, accumulated)
			}

			// Verify final detection matches expected
			detected, agent, command := DetectPrompt(accumulated)
			if !detected {
				t.Error("Final accumulated text should be detected as prompt")
			}
			if agent != "claude" {
				t.Errorf("agent = %q, want %q", agent, "claude")
			}
			if command != "docker-compose up -d" {
				t.Errorf("command = %q, want %q", command, "docker-compose up -d")
			}
		})
	}
}

// TestDetectPrompt_ConcurrentSessions simulates multiple interleaved agent outputs
func TestDetectPrompt_ConcurrentSessions(t *testing.T) {
	// In reality, orkestra spawns agents in separate PTYs, but this tests
	// that patterns don't cross-contaminate if output were mixed
	claudePrompt := "Allow claude to execute this Bash command? git push [Y/n]"
	codexPrompt := "Run command: npm test [Y/n]"
	regularOutput := "Analyzing codebase... 50% complete"

	inputs := []struct {
		text     string
		expected bool
	}{
		{claudePrompt, true},
		{regularOutput, false},
		{codexPrompt, true},
		{regularOutput, false},
		{claudePrompt, true},
	}

	for i, input := range inputs {
		detected, _, _ := DetectPrompt(input.text)
		if detected != input.expected {
			t.Errorf("Input %d: detected = %v, want %v\nText: %q",
				i+1, detected, input.expected, input.text)
		}
	}
}

// TestIsPermissionPrompt_QuickCheck validates the convenience function
func TestIsPermissionPrompt_QuickCheck(t *testing.T) {
	prompts := []string{
		"Allow claude to execute this Bash command? git status [Y/n]",
		"Run command: npm test [Y/n]",
		"Allow claude to call mcp__orkestra__notify?",
	}

	nonPrompts := []string{
		"Regular output",
		"On branch main",
		"",
		"The claude agent is running",
		"Run command: npm test", // Codex without [Y/n] should not match
	}

	for _, p := range prompts {
		if !isPermissionPrompt(p) {
			t.Errorf("isPermissionPrompt(%q) = false, want true", p)
		}
	}

	for _, np := range nonPrompts {
		if isPermissionPrompt(np) {
			t.Errorf("isPermissionPrompt(%q) = true, want false", np)
		}
	}
}

// TestExtractCommand_QuickCheck validates command extraction convenience function
func TestExtractCommand_QuickCheck(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Allow claude to execute this Bash command? git status [Y/n]", "git status"},
		{"Run command: npm install [Y/n]", "npm install"},
		{"Allow claude to call WebSearch?", "WebSearch"},
		{"Regular output", ""},
		{"", ""},
	}

	for _, tt := range tests {
		got := extractCommand(tt.input)
		if got != tt.want {
			t.Errorf("extractCommand(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
