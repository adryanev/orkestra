// Package risk defines command execution risk levels for Orkestra.
package risk

// Level represents the risk level of a command execution.
type Level int

const (
	// Safe commands have no destructive potential (ls, cat, grep, git status, etc.)
	Safe Level = iota
	// Moderate commands can modify state but are generally recoverable (git push, npm publish, docker, cloud CLIs)
	Moderate
	// Dangerous commands can cause irreversible damage or security issues (rm -rf, sudo, dd, curl | bash, etc.)
	Dangerous
)

// String returns the string representation of a Level.
func (r Level) String() string {
	switch r {
	case Safe:
		return "Safe"
	case Moderate:
		return "Moderate"
	case Dangerous:
		return "Dangerous"
	default:
		return "Unknown"
	}
}
