package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/adryanev/orkestra/pkg/mcp"
	"github.com/adryanev/orkestra/pkg/process"
	"github.com/adryanev/orkestra/pkg/runner"
	"github.com/adryanev/orkestra/pkg/workspace"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var ( 
	cfgFile string
	configDir string
	wm *workspace.Manager
	pm *process.ProcessManager
	agentRunner *runner.Runner
	mcpServer *mcp.Server
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

		pm = process.NewProcessManager()
		agentRunner = runner.NewRunner(wm)
		mcpServer = mcp.NewServer(wm)

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

	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(workspaceCmd)
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(resumeCmd)
	rootCmd.AddCommand(stopCmd)
	rootCmd.AddCommand(mcpCmd)
}

func initConfig() {
	if cfgFile != "" {
		vpr := viper.New()
		vpr.SetConfigFile(cfgFile)
		if err := vpr.ReadInConfig(); err != nil {
			cobra.CheckErr(err)
		}
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error finding home directory: %v\n", err)
			os.Exit(1)
		}

		configDir = filepath.Join(home, ".orkestra")
		vpr := viper.New()
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

	vpr := viper.GetViper()
	vpr.SetEnvPrefix("XORKESTRA")
	vpr.AutomaticEnv()
}

func printJSON(data interface{}) {
	fmt.Println(data)
}
