// Package pty provides command risk classification for intelligent auto-approval.
package pty

import (
	"path/filepath"
	"strings"

	"github.com/acarl005/stripansi"
	"github.com/adryanev/orkestra/pkg/risk"
)

// RiskLevel is an alias for risk.Level for backward compatibility.
// Deprecated: Use risk.Level directly.
type RiskLevel = risk.Level

const (
	// Safe commands have no destructive potential (ls, cat, grep, git status, etc.)
	Safe = risk.Safe
	// Moderate commands can modify state but are generally recoverable (git push, npm publish, docker, cloud CLIs)
	Moderate = risk.Moderate
	// Dangerous commands can cause irreversible damage or security issues (rm -rf, sudo, dd, curl | bash, etc.)
	Dangerous = risk.Dangerous
)

// dangerousCommands are commands that can cause irreversible damage or security issues.
var dangerousCommands = map[string]bool{
	"rm":        true,
	"dd":        true,
	"mkfs":      true,
	"fdisk":     true,
	"parted":    true,
	"shutdown":  true,
	"reboot":    true,
	"halt":      true,
	"poweroff":  true,
	"init":      true,
	"iptables":  true,
	"ip6tables": true,
	"sudo":      true,
	"su":        true,
	"chown":     true,
	"chmod":     true,
	"kill":      true,
	"killall":   true,
	"pkill":     true,
	"format":    true,
	"del":       true, // Windows
	"erase":     true, // Windows
}

// moderateCommands are commands that modify state but are generally recoverable.
// These are only considered moderate when combined with dangerous operations.
var moderateCommands = map[string]bool{
	"npm":       true,
	"yarn":      true,
	"pnpm":      true,
	"docker":    true,
	"podman":    true,
	"kubectl":   true,
	"helm":      true,
	"terraform": true,
	"aws":       true,
	"gcloud":    true,
	"az":        true,
	"heroku":    true,
	"fly":       true,
	"vercel":    true,
	"cargo":     true,
	"go":        true,
	"make":      true,
	"cmake":     true,
	"pip":       true,
	"mvn":       true,
	"gradle":    true,
}

// dangerousPatterns are patterns that indicate dangerous operations.
var dangerousPatterns = []struct {
	tokens []string
	check  func(tokens []string) bool
}{
	// rm -rf or rm -f
	{
		tokens: []string{"rm"},
		check: func(tokens []string) bool {
			for _, t := range tokens {
				if strings.HasPrefix(t, "-") && (strings.Contains(t, "r") || strings.Contains(t, "f")) {
					return true
				}
			}
			return false
		},
	},
	// curl/wget piped to bash/sh
	{
		tokens: []string{},
		check: func(tokens []string) bool {
			hasCurlOrWget := false
			hasPipe := false
			hasShell := false
			for i, t := range tokens {
				base := normalizeExecutable(t)
				if base == "curl" || base == "wget" {
					hasCurlOrWget = true
				}
				if t == "|" {
					hasPipe = true
				}
				if base == "bash" || base == "sh" || base == "zsh" || base == "fish" {
					hasShell = true
				}
				// Check for curl | bash pattern
				if hasCurlOrWget && i > 0 && tokens[i-1] == "|" && hasShell {
					return true
				}
			}
			return hasCurlOrWget && hasPipe && hasShell
		},
	},
	// Commands with > /dev/sd* or > /dev/disk*
	{
		tokens: []string{},
		check: func(tokens []string) bool {
			for i, t := range tokens {
				if (t == ">" || t == ">>") && i+1 < len(tokens) {
					next := tokens[i+1]
					if strings.HasPrefix(next, "/dev/sd") || strings.HasPrefix(next, "/dev/disk") ||
						strings.HasPrefix(next, "/dev/nvme") {
						return true
					}
				}
			}
			return false
		},
	},
}

// moderatePatterns are patterns that indicate moderate-risk operations.
var moderatePatterns = []struct {
	command string
	flags   []string
}{
	{"git", []string{"push", "--force", "-f"}},
	{"git", []string{"push", "--force-with-lease"}},
	{"git", []string{"reset", "--hard"}},
	{"git", []string{"clean", "-f", "-d", "-x"}},
	{"npm", []string{"publish"}},
	{"yarn", []string{"publish"}},
	{"pnpm", []string{"publish"}},
	{"cargo", []string{"publish"}},
	{"docker", []string{"rm", "rmi", "prune"}},
	{"kubectl", []string{"delete", "apply", "create"}},
	{"terraform", []string{"apply", "destroy"}},
	{"aws", []string{"delete", "terminate", "remove"}},
	{"gcloud", []string{"delete", "remove"}},
}

// ClassifyRisk analyzes a command string and returns its risk level.
// The function tokenizes the command, checks for dangerous keywords,
// and examines flag combinations to determine the highest applicable risk level.
func ClassifyRisk(command string) risk.Level {
	if command == "" {
		return Safe
	}

	clean := stripansi.Strip(command)
	tokens := tokenize(clean)
	if len(tokens) == 0 {
		return Safe
	}

	baseCommand := normalizeExecutable(tokens[0])

	// Check for dangerous commands first (exact match or prefix for dotted commands like mkfs.ext4)
	if dangerousCommands[baseCommand] {
		return Dangerous
	}
	// Check for dangerous command prefixes (e.g., mkfs.ext4 matches mkfs)
	for dangerousCmd := range dangerousCommands {
		if strings.HasPrefix(baseCommand, dangerousCmd+".") {
			return Dangerous
		}
	}

	// Check for dangerous patterns
	for _, pattern := range dangerousPatterns {
		if len(pattern.tokens) == 0 || containsAny(tokens, pattern.tokens) {
			if pattern.check(tokens) {
				return Dangerous
			}
		}
	}

	// Special handling for git - check for dangerous operations
	if baseCommand == "git" {
		for _, pattern := range moderatePatterns {
			if pattern.command == "git" && containsAny(tokens, pattern.flags) {
				return Moderate
			}
		}
		// Safe git commands (status, log, diff, etc.)
		return Safe
	}

	// Check for moderate commands with dangerous flags
	if moderateCommands[baseCommand] {
		for _, pattern := range moderatePatterns {
			if pattern.command == baseCommand && containsAny(tokens, pattern.flags) {
				return Moderate
			}
		}
		// Base moderate commands without dangerous flags
		return Moderate
	}

	// Default to safe
	return Safe
}

func normalizeExecutable(token string) string {
	if token == "" {
		return ""
	}
	return filepath.Base(token)
}

// tokenize splits a command string into tokens, handling quotes and escapes.
func tokenize(command string) []string {
	var tokens []string
	var current strings.Builder
	var inSingleQuote, inDoubleQuote, escaped bool

	for i := 0; i < len(command); i++ {
		ch := command[i]

		if escaped {
			current.WriteByte(ch)
			escaped = false
			continue
		}

		switch ch {
		case '\\':
			escaped = true
			continue
		case '\'':
			if !inDoubleQuote {
				inSingleQuote = !inSingleQuote
			} else {
				current.WriteByte(ch)
			}
		case '"':
			if !inSingleQuote {
				inDoubleQuote = !inDoubleQuote
			} else {
				current.WriteByte(ch)
			}
		case ' ', '\t', '\n':
			if inSingleQuote || inDoubleQuote {
				current.WriteByte(ch)
			} else if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		case '|', '>', '<', ';', '&':
			// Special shell operators - treat as separate tokens
			if inSingleQuote || inDoubleQuote {
				current.WriteByte(ch)
			} else {
				if current.Len() > 0 {
					tokens = append(tokens, current.String())
					current.Reset()
				}
				// Check for multi-character operators (>>, &&, ||)
				if i+1 < len(command) && (command[i+1] == ch || (ch == '>' && command[i+1] == '>')) {
					tokens = append(tokens, string([]byte{ch, command[i+1]}))
					i++
				} else {
					tokens = append(tokens, string(ch))
				}
			}
		default:
			current.WriteByte(ch)
		}
	}

	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}

	return tokens
}

// containsAny returns true if tokens contains any of the target strings.
func containsAny(tokens []string, targets []string) bool {
	for _, token := range tokens {
		for _, target := range targets {
			if token == target {
				return true
			}
		}
	}
	return false
}
