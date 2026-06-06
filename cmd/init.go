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
			fmt.Fprintf(cmd.ErrOrStderr(), "Error: %v\n", err)
			os.Exit(1)
		}

		// Create empty state files
		initJSON := "{}"
		for _, f := range []string{"workspaces.json", "sessions.json"} {
			path := filepath.Join(dir, f)
			if _, err := os.Stat(path); os.IsNotExist(err) {
				os.WriteFile(path, []byte(initJSON), 0644)
			}
		}
		// todos.json is a JSON array, not an object.
		todosPath := filepath.Join(dir, "todos.json")
		if _, err := os.Stat(todosPath); os.IsNotExist(err) {
			os.WriteFile(todosPath, []byte("[]"), 0644)
		}

		fmt.Printf("Orkestra initialized at %s\n", dir)
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