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

// PromptPattern defines a pattern for detecting permission prompts from an agent.
type PromptPattern struct {
	Agent            string                           // Agent identifier (e.g., "claude", "codex")
	Pattern          *regexp.Regexp                   // Compiled regex pattern to match prompt text
	CommandExtractor func(match []string) string      // Function to extract command from regex capture groups
}

// Predefined patterns for known agents.
var patterns = []PromptPattern{
	// Claude Code permission prompt pattern (multiline support with [\s\S] to match newlines)
	// Example: "Allow claude to execute this Bash command?\n  git status"
	// Also handles multiline commands with backslash continuation
	// Stops when it encounters:
	//   - \n\s*\[ (newline then bracket - for [Y/n] on separate line)
	//   - \s+\[[YyNn/] (space then bracket starting [Y/n] - inline prompt)
	//   - $ (end of string)
	{
		Agent:   "claude",
		Pattern: regexp.MustCompile(`(?i)Allow\s+claude\s+to\s+execute\s+this\s+Bash\s+command\?\s*\n?\s*([\s\S]+?)(?:\n\s*\[|\s+\[[YyNn/]|$)`),
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
	// Codex permission prompt pattern (stops at newline or box chars)
	// Example: "Run command: git status"
	// Box drawing chars: │ ╰ ╭ ─ ╮ ╯
	{
		Agent:   "codex",
		Pattern: regexp.MustCompile(`(?i)Run\s+command:\s*([^\n\[│╰╭─╮╯]+)`),
		CommandExtractor: func(match []string) string {
			if len(match) > 1 {
				return strings.TrimSpace(match[1])
			}
			return ""
		},
	},
	// Codex approval prompt pattern (stops at newline or box chars)
	// Example: "Execute: npm install [Y/n]"
	{
		Agent:   "codex",
		Pattern: regexp.MustCompile(`(?i)Execute:\s*([^\n\[│╰╭─╮╯]+)`),
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

// IsPermissionPrompt is a convenience function that returns only whether
// the line contains a permission prompt, without extracting details.
func IsPermissionPrompt(line string) bool {
	detected, _, _ := DetectPrompt(line)
	return detected
}

// ExtractCommand attempts to extract the command from a line that was
// previously identified as a permission prompt. Returns empty string if
// no command can be extracted.
func ExtractCommand(line string) string {
	_, _, command := DetectPrompt(line)
	return command
}
