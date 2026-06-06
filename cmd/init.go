package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

const (
	DefaultConfigDir = ".orkestra"
	WorkspacesFile   = "workspaces.json"
	SessionsFile     = "sessions.json"
)
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