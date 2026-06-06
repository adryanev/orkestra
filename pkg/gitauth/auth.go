package gitauth

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// resolveTimeout bounds the `gh auth token` call so a stuck gh process (network
// auth, credential helper prompt) cannot hang the command that needs the token.
const resolveTimeout = 10 * time.Second

// execCommandContext is overridable in tests.
var execCommandContext = exec.CommandContext

// ResolveToken gets the GH token for a specific gh profile under a default
// deadline. Runs: gh auth token --user <profile>.
func ResolveToken(profile string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), resolveTimeout)
	defer cancel()
	return ResolveTokenContext(ctx, profile)
}

// ResolveTokenContext resolves the token under the caller's context, so a
// timeout or cancellation aborts the gh call rather than blocking.
func ResolveTokenContext(ctx context.Context, profile string) (string, error) {
	cmd := execCommandContext(ctx, "gh", "auth", "token", "--user", profile)
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
