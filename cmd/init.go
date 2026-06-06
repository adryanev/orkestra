package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize Orkestra environment",
	Run: func(cmd *cobra.Command, args []string) {
		configDir := getConfigDir()
		if err := os.MkdirAll(configDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to create %s: %v\n", configDir, err)
			os.Exit(1)
		}

		// Create empty workspaces.json and sessions.json if they don't exist
		workspacesPath := filepath.Join(configDir, WorkspacesFile)
		if _, err := os.Stat(workspacesPath); os.IsNotExist(err) {
			if err := os.WriteFile(workspacesPath, []byte("[]"), 0644); err != nil {
				fmt.Fprintf(os.Stderr, "Error: failed to create %s: %v\n", workspacesPath, err)
				os.Exit(1)
			}
		}

		sessionsPath := filepath.Join(configDir, SessionsFile)
		if _, err := os.Stat(sessionsPath); os.IsNotExist(err) {
			if err := os.WriteFile(sessionsPath, []byte("[]"), 0644); err != nil {
				fmt.Fprintf(os.Stderr, "Error: failed to create %s: %v\n", sessionsPath, err)
				os.Exit(1)
			}
		}

		fmt.Printf("Orkestra initialized at %s\n", configDir)
	},
}

func getConfigDir() string {
	if configDir != "" {
		return configDir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	return home + "/.orkestra"
}