package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/adryanev/orkestra/pkg/runner"
	"github.com/adryanev/orkestra/pkg/workspace"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cfgFile     string
	configDir   string
	jsonOutput  bool
	wm          *workspace.Manager
	agentRunner *runner.Runner
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "orkestra",
	Short: "Orkestra CLI for AI agent orchestration",
	Long: `Orkestra CLI for AI agent orchestration.

This tool provides capabilities for managing AI agent workspaces,
running agents, and interacting with them via the MCP protocol.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		initConfig()
		var err error
		wm, err = workspace.NewManager(configDir)
		if err != nil {
			return fmt.Errorf("failed to initialize workspace manager: %w", err)
		}

		agentRunner = runner.NewRunner(wm)

		return nil
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.orkestra/.orkestra.yaml)")
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "emit structured JSON output")

	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(workspaceCmd)
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(resumeCmd)
	rootCmd.AddCommand(stopCmd)
	rootCmd.AddCommand(mcpCmd)
	rootCmd.AddCommand(todoCmd)
}

func initConfig() {
	// Resolve the single config directory: XORKESTRA_HOME wins, otherwise
	// ~/.orkestra. Every state file resolves under this directory (R7).
	configDir = resolveConfigDir()

	vpr := viper.GetViper()
	vpr.SetEnvPrefix("XORKESTRA")
	vpr.AutomaticEnv()

	if cfgFile != "" {
		vpr.SetConfigFile(cfgFile)
		if err := vpr.ReadInConfig(); err != nil {
			cobra.CheckErr(err)
		}
	} else {
		vpr.AddConfigPath(configDir)
		vpr.SetConfigName(".orkestra")
		vpr.SetConfigType("yaml")

		if err := vpr.ReadInConfig(); err == nil {
			fmt.Fprintln(os.Stderr, "Using config file:", vpr.ConfigFileUsed())
		} else {
			if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
				cobra.CheckErr(err)
			}
		}
	}
}

// resolveConfigDir returns XORKESTRA_HOME when set, otherwise ~/.orkestra.
func resolveConfigDir() string {
	if home := os.Getenv("XORKESTRA_HOME"); home != "" {
		return home
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error finding home directory: %v\n", err)
		os.Exit(1)
	}
	return filepath.Join(home, ".orkestra")
}

// emitResult prints either a human-readable line or, under --json, the data
// value as indented JSON on stdout.
func emitResult(human string, data interface{}) {
	if jsonOutput {
		b, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			emitError(fmt.Errorf("failed to encode JSON output: %w", err))
			return
		}
		fmt.Println(string(b))
		return
	}
	fmt.Println(human)
}

// emitError reports err and exits non-zero. Under --json it writes a structured
// {"error": "..."} object to stderr; otherwise a plain "Error: ..." line.
func emitError(err error) {
	if jsonOutput {
		b, _ := json.MarshalIndent(map[string]string{"error": err.Error()}, "", "  ")
		fmt.Fprintln(os.Stderr, string(b))
	} else {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	}
	os.Exit(1)
}
