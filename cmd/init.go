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
		dir := getConfigDir()
		if err := os.MkdirAll(dir, 0755); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Error: %v\n", err)
			os.Exit(1)
		}

		// Create empty state files
		initJSON := "{}"
		for _, f := range []string{"workspaces.json", "sessions.json"} {
			path := dir + "/" + f
			if _, err := os.Stat(path); os.IsNotExist(err) {
				os.WriteFile(path, []byte(initJSON), 0644)
			}
		}

		fmt.Printf("Orkestra initialized at %s\n", dir)
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