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