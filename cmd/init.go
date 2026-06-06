package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize Orkestra environment",
	Run: func(cmd *cobra.Command, args []string) {
		dir := getConfigDir()
		if err := os.MkdirAll(dir, 0755); err != nil {
			emitError(err)
		}

		// Create empty state files. Object-shaped state starts as {}, todos as
		// a JSON array. All resolve under the one config directory (R7).
		for _, f := range []string{"workspaces.json", "sessions.json"} {
			path := filepath.Join(dir, f)
			if _, err := os.Stat(path); err != nil {
				if !os.IsNotExist(err) {
					emitError(err)
				}
				if err := os.WriteFile(path, []byte("{}"), 0644); err != nil {
					emitError(err)
				}
			}
		}
		todosPath := filepath.Join(dir, "todos.json")
		if _, err := os.Stat(todosPath); err != nil {
			if !os.IsNotExist(err) {
				emitError(err)
			}
			if err := os.WriteFile(todosPath, []byte("[]"), 0644); err != nil {
				emitError(err)
			}
		}

		emitResult(
			fmt.Sprintf("Orkestra initialized at %s", dir),
			map[string]string{"status": "initialized", "path": dir},
		)
	},
}

// getConfigDir returns the resolved config directory, preferring an already
// resolved configDir and otherwise falling back to the shared resolver so the
// XORKESTRA_HOME / ~/.orkestra logic lives in one place (resolveConfigDir).
func getConfigDir() string {
	if configDir != "" {
		return configDir
	}
	return resolveConfigDir()
}
