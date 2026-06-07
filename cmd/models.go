package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var (
	modelsAgent string
)

// Model represents an AI model with its metadata
type Model struct {
	Name        string `json:"name"`
	Agent       string `json:"agent"`
	Description string `json:"description"`
	Tier        string `json:"tier,omitempty"`
}

// getAvailableModels returns the list of available models for Claude Code and Codex
func getAvailableModels() []Model {
	return []Model{
		// Claude Code Models
		{
			Name:        "claude-sonnet-4-5",
			Agent:       "claude",
			Description: "Fast and efficient, great for most tasks",
			Tier:        "default",
		},
		{
			Name:        "claude-opus-4-8",
			Agent:       "claude",
			Description: "Most capable model for complex reasoning",
			Tier:        "premium",
		},
		{
			Name:        "claude-3-5-sonnet",
			Agent:       "claude",
			Description: "Previous generation sonnet (legacy)",
			Tier:        "legacy",
		},
		{
			Name:        "claude-3-7-sonnet",
			Agent:       "claude",
			Description: "Enhanced sonnet with improved reasoning",
			Tier:        "standard",
		},

		// Codex Models
		{
			Name:        "gpt-5-codex-spark",
			Agent:       "codex",
			Description: "Fast coding assistant, optimized for speed",
			Tier:        "default",
		},
		{
			Name:        "gpt-5.3-codex",
			Agent:       "codex",
			Description: "Balanced performance and quality",
			Tier:        "standard",
		},
		{
			Name:        "gpt-5.5-codex-xhigh",
			Agent:       "codex",
			Description: "Highest quality for complex code review",
			Tier:        "premium",
		},
	}
}

var modelsCmd = &cobra.Command{
	Use:   "models",
	Short: "List available AI models for Claude Code and Codex",
	Run: func(cmd *cobra.Command, args []string) {
		allModels := getAvailableModels()
		var filtered []Model

		// Filter by agent if specified
		if modelsAgent != "" && modelsAgent != "all" {
			switch modelsAgent {
			case "claude":
				for _, m := range allModels {
					if m.Agent == "claude" {
						filtered = append(filtered, m)
					}
				}
			case "codex":
				for _, m := range allModels {
					if m.Agent == "codex" {
						filtered = append(filtered, m)
					}
				}
			default:
				emitError(fmt.Errorf("invalid --agent %q (expected claude, codex, or all)", modelsAgent))
			}
		} else {
			filtered = allModels
		}

		if jsonOutput {
			// Output as JSON
			emitResult("", map[string]interface{}{
				"models": filtered,
			})
		} else {
			// Output as formatted text
			printModelsTable(filtered)
		}
	},
}

func printModelsTable(models []Model) {
	// Group by agent
	claudeModels := []Model{}
	codexModels := []Model{}

	for _, m := range models {
		if m.Agent == "claude" {
			claudeModels = append(claudeModels, m)
		} else if m.Agent == "codex" {
			codexModels = append(codexModels, m)
		}
	}

	if len(claudeModels) > 0 {
		fmt.Println("Claude Code Models:")
		fmt.Println(strings.Repeat("=", 60))
		for _, m := range claudeModels {
			tier := ""
			if m.Tier != "" {
				tier = fmt.Sprintf(" [%s]", m.Tier)
			}
			fmt.Printf("  %-25s %s%s\n", m.Name, m.Description, tier)
		}
		fmt.Println()
	}

	if len(codexModels) > 0 {
		fmt.Println("Codex Models:")
		fmt.Println(strings.Repeat("=", 60))
		for _, m := range codexModels {
			tier := ""
			if m.Tier != "" {
				tier = fmt.Sprintf(" [%s]", m.Tier)
			}
			fmt.Printf("  %-25s %s%s\n", m.Name, m.Description, tier)
		}
		fmt.Println()
	}

	// Print usage hint
	fmt.Println("Usage:")
	fmt.Println("  orkestra run --workspace <id> --model <model-name> --prompt \"...\"")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  orkestra run --workspace fix-auth --model claude-sonnet-4-5 --prompt \"Fix auth\"")
	fmt.Println("  orkestra run --workspace fix-tests --model gpt-5-codex-spark --prompt \"Fix tests\"")
}

func init() {
	modelsCmd.Flags().StringVar(&modelsAgent, "agent", "all", "Filter by agent (claude, codex, or all)")
	rootCmd.AddCommand(modelsCmd)
}
