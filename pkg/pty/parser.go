// Package pty provides utilities for parsing and detecting permission prompts
// from agent PTY output streams (Claude Code, Codex, etc.).
//
// Example usage:
//
//	line := "Allow claude to execute this Bash command? git status"
//	if detected, agent, cmd := DetectPrompt(line); detected {
//	    fmt.Printf("Agent %s wants to run: %s\n", agent, cmd)
//	    // Auto-approve or ask user for confirmation
//	}
package pty

import (
	"regexp"
	"strings"
)

const approvalTokenPattern = `\[[Yy]/[Nn]\]`

// PromptPattern defines a pattern for detecting permission prompts from an agent.
type PromptPattern struct {
	Agent            string                      // Agent identifier (e.g., "claude", "codex")
	Pattern          *regexp.Regexp              // Compiled regex pattern to match prompt text
	CommandExtractor func(match []string) string // Function to extract command from regex capture groups
}

// Predefined patterns for known agents.
var patterns = []PromptPattern{
	// Claude Code permission prompt pattern. Anchored to a prompt line and
	// requiring an explicit [Y/n] gate to avoid matching ordinary output.
	// Example: "Allow claude to execute this Bash command?\n  git status\n[Y/n]"
	// Also handles multiline commands with backslash continuation and inline
	// or separate-line approval tokens.
	{
		Agent:   "claude",
		Pattern: regexp.MustCompile(`(?im)^Allow\s+claude\s+to\s+execute\s+this\s+Bash\s+command\?\s*\n?\s*([\s\S]+?)\s*(?:\n\s*)?` + approvalTokenPattern),
		CommandExtractor: func(match []string) string {
			if len(match) > 1 {
				return strings.TrimSpace(match[1])
			}
			return ""
		},
	},
	// Claude Code MCP/tool permission prompts
	// Example: "Allow claude to call mcp__orkestra__notify?"
	{
		Agent:   "claude",
		Pattern: regexp.MustCompile(`(?i)Allow\s+claude\s+to\s+call\s+([a-zA-Z0-9_]+(?:__[a-zA-Z0-9_]+)*)\??`),
		CommandExtractor: func(match []string) string {
			if len(match) > 1 {
				return strings.TrimSpace(match[1])
			}
			return ""
		},
	},
	// Codex permission prompt pattern, anchored to line start to prevent
	// mid-paragraph false positives. Requires [Y/n] after the command so
	// general "Run command:" log lines are not mistaken for prompts.
	// Example: "Run command: git status [Y/n]"
	{
		Agent:   "codex",
		Pattern: regexp.MustCompile(`(?im)^Run\s+command:\s*([^\n\[│╰╭─╮╯]+)\s*` + approvalTokenPattern),
		CommandExtractor: func(match []string) string {
			if len(match) > 1 {
				return strings.TrimSpace(match[1])
			}
			return ""
		},
	},
	// Codex approval prompt pattern, anchored to line start.
	// Requires [Y/n] after the command.
	// Example: "Execute: npm install [Y/n]"
	{
		Agent:   "codex",
		Pattern: regexp.MustCompile(`(?im)^Execute:\s*([^\n\[│╰╭─╮╯]+)\s*` + approvalTokenPattern),
		CommandExtractor: func(match []string) string {
			if len(match) > 1 {
				return strings.TrimSpace(match[1])
			}
			return ""
		},
	},
}

// DetectPrompt analyzes a line of text to determine if it contains a permission prompt
// from a known agent. Returns:
//   - detected: true if a prompt was found
//   - agent: the agent identifier (e.g., "claude", "codex")
//   - command: the extracted command or tool name from the prompt
func DetectPrompt(line string) (detected bool, agent, command string) {
	for _, p := range patterns {
		if matches := p.Pattern.FindStringSubmatch(line); matches != nil {
			cmd := p.CommandExtractor(matches)
			if cmd != "" {
				return true, p.Agent, cmd
			}
		}
	}
	return false, "", ""
}
