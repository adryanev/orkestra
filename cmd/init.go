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
			if _, err := os.Stat(path); os.IsNotExist(err) {
				if err := os.WriteFile(path, []byte("{}"), 0644); err != nil {
					emitError(err)
				}
			}
		}
		todosPath := filepath.Join(dir, "todos.json")
		if _, err := os.Stat(todosPath); os.IsNotExist(err) {
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

func getConfigDir() string {
	if configDir != "" {
		return configDir
	}
	if home := os.Getenv("XORKESTRA_HOME"); home != "" {
		return home
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	return filepath.Join(home, ".orkestra")
}
