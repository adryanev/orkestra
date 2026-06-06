package gitauth

import (
	"fmt"
	"os/exec"
	"strings"
)

// ResolveToken gets the GH token for a specific gh profile.
// Runs: gh auth token --user <profile>
// Returns the trimmed token string or error.
func ResolveToken(profile string) (string, error) {
	cmd := exec.Command("gh", "auth", "token", "--user", profile)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to resolve token for profile %q: %w", profile, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// BuildEnvVars returns env var pairs for git authentication.
// These should be injected into spawned agent processes.
func BuildEnvVars(token string) []string {
	return []string{"GH_TOKEN=" + token}
}