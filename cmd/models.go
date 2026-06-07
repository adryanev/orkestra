package cmd

import (
	"fmt"
	"os/exec"
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
	Source      string `json:"source,omitempty"` // "detected" or "fallback"
}

// detectClaudeModels tries to detect available Claude models from the CLI
func detectClaudeModels() []Model {
	models := []Model{}

	// Try to get models from claude CLI help or version info
	cmd := exec.Command("claude", "--help")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil
	}

	helpText := string(output)

	// Common Claude model patterns to look for
	claudeModelPatterns := []struct {
		name string
		desc string
		tier string
	}{
		{"claude-sonnet-4-5", "Fast and efficient, great for most tasks", "default"},
		{"claude-opus-4-8", "Most capable model for complex reasoning", "premium"},
		{"claude-3-7-sonnet", "Enhanced sonnet with improved reasoning", "standard"},
		{"claude-3-5-sonnet", "Previous generation sonnet (legacy)", "legacy"},
	}

	// Check if CLI mentions specific models in help
	for _, pattern := range claudeModelPatterns {
		if strings.Contains(helpText, pattern.name) ||
			strings.Contains(helpText, strings.Split(pattern.name, "-")[1]) {
			models = append(models, Model{
				Name:        pattern.name,
				Agent:       "claude",
				Description: pattern.desc,
				Tier:        pattern.tier,
				Source:      "detected",
			})
		}
	}

	// If no models detected, return nil to use fallback
	if len(models) == 0 {
		return nil
	}

	return models
}

// detectCodexModels tries to detect available Codex models from the CLI
func detectCodexModels() []Model {
	models := []Model{}

	// Try to get models from codex CLI
	cmd := exec.Command("codex", "--help")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil
	}

	helpText := string(output)

	// Common Codex model patterns
	codexModelPatterns := []struct {
		name string
		desc string
		tier string
	}{
		{"gpt-5-codex-spark", "Fast coding assistant, optimized for speed", "default"},
		{"gpt-5.3-codex", "Balanced performance and quality", "standard"},
		{"gpt-5.5-codex-xhigh", "Highest quality for complex code review", "premium"},
	}

	for _, pattern := range codexModelPatterns {
		if strings.Contains(helpText, pattern.name) ||
			strings.Contains(helpText, strings.Split(pattern.name, "-")[0]) {
			models = append(models, Model{
				Name:        pattern.name,
				Agent:       "codex",
				Description: pattern.desc,
				Tier:        pattern.tier,
				Source:      "detected",
			})
		}
	}

	if len(models) == 0 {
		return nil
	}

	return models
}

// getFallbackModels returns hardcoded fallback models
func getFallbackModels() []Model {
	return []Model{
		// Claude Code Models
		{
			Name:        "claude-sonnet-4-5",
			Agent:       "claude",
			Description: "Fast and efficient, great for most tasks",
			Tier:        "default",
			Source:      "fallback",
		},
		{
			Name:        "claude-opus-4-8",
			Agent:       "claude",
			Description: "Most capable model for complex reasoning",
			Tier:        "premium",
			Source:      "fallback",
		},
		{
			Name:        "claude-3-5-sonnet",
			Agent:       "claude",
			Description: "Previous generation sonnet (legacy)",
			Tier:        "legacy",
			Source:      "fallback",
		},
		{
			Name:        "claude-3-7-sonnet",
			Agent:       "claude",
			Description: "Enhanced sonnet with improved reasoning",
			Tier:        "standard",
			Source:      "fallback",
		},

		// Codex Models
		{
			Name:        "gpt-5-codex-spark",
			Agent:       "codex",
			Description: "Fast coding assistant, optimized for speed",
			Tier:        "default",
			Source:      "fallback",
		},
		{
			Name:        "gpt-5.3-codex",
			Agent:       "codex",
			Description: "Balanced performance and quality",
			Tier:        "standard",
			Source:      "fallback",
		},
		{
			Name:        "gpt-5.5-codex-xhigh",
			Agent:       "codex",
			Description: "Highest quality for complex code review",
			Tier:        "premium",
			Source:      "fallback",
		},
	}
}

// getAvailableModels returns the list of available models, auto-detected from CLI if possible
func getAvailableModels() []Model {
	allModels := []Model{}

	// Try to detect Claude models
	claudeModels := detectClaudeModels()
	if claudeModels == nil {
		// Use fallback for Claude
		for _, m := range getFallbackModels() {
			if m.Agent == "claude" {
				claudeModels = append(claudeModels, m)
			}
		}
	}
	allModels = append(allModels, claudeModels...)

	// Try to detect Codex models
	codexModels := detectCodexModels()
	if codexModels == nil {
		// Use fallback for Codex
		for _, m := range getFallbackModels() {
			if m.Agent == "codex" {
				codexModels = append(codexModels, m)
			}
		}
	}
	allModels = append(allModels, codexModels...)

	return allModels
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
			source := ""
			if m.Source == "detected" {
				source = " ✓"
			}
			fmt.Printf("  %-25s %s%s%s\n", m.Name, m.Description, tier, source)
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
			source := ""
			if m.Source == "detected" {
				source = " ✓"
			}
			fmt.Printf("  %-25s %s%s%s\n", m.Name, m.Description, tier, source)
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
