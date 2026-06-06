package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

type Todo struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
	WorkspaceID string `json:"workspace_id,omitempty"`
}

var todoFile string

func init() {
	home, _ := os.UserHomeDir()
	todoFile = filepath.Join(home, ".orkestra", "todos.json")
}

var todoCmd = &cobra.Command{
	Use:   "todo",
	Short: "Manage todos",
}

var todoCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new todo",
	Run: func(cmd *cobra.Command, args []string) {
		title, _ := cmd.Flags().GetString("title")
		desc, _ := cmd.Flags().GetString("description")
		ws, _ := cmd.Flags().GetString("workspace")

		if title == "" {
			fmt.Fprintln(cmd.ErrOrStderr(), "Error: --title is required")
			os.Exit(1)
		}

		todos := loadTodos()
		todo := Todo{
			ID:          uuid.New().String(),
			Title:       title,
			Description: desc,
			Status:      "todo",
			CreatedAt:   time.Now().UTC().Format(time.RFC3339),
			WorkspaceID: ws,
		}
		todos = append(todos, todo)
		saveTodos(todos)

		data, _ := json.MarshalIndent(todo, "", "  ")
		fmt.Println(string(data))
	},
}

var todoListCmd = &cobra.Command{
	Use:   "list",
	Short: "List todos",
	Run: func(cmd *cobra.Command, args []string) {
		statusFilter, _ := cmd.Flags().GetString("status")
		wsFilter, _ := cmd.Flags().GetString("workspace")
		asJSON, _ := cmd.Flags().GetBool("json")

		todos := loadTodos()
		var filtered []Todo
		for _, t := range todos {
			if statusFilter != "" && t.Status != statusFilter {
				continue
			}
			if wsFilter != "" && t.WorkspaceID != wsFilter {
				continue
			}
			filtered = append(filtered, t)
		}

		if asJSON {
			data, _ := json.MarshalIndent(filtered, "", "  ")
			fmt.Println(string(data))
			return
		}

		if len(filtered) == 0 {
			fmt.Println("No todos found")
			return
		}
		for _, t := range filtered {
			fmt.Printf("  %s | %s | %s | %s\n", t.ID[:8], t.Status, t.Title, t.CreatedAt[:10])
		}
	},
}

var todoUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update a todo",
	Run: func(cmd *cobra.Command, args []string) {
		id, _ := cmd.Flags().GetString("id")
		title, _ := cmd.Flags().GetString("title")
		desc, _ := cmd.Flags().GetString("description")
		status, _ := cmd.Flags().GetString("status")

		if id == "" {
			fmt.Fprintln(cmd.ErrOrStderr(), "Error: --id is required")
			os.Exit(1)
		}

		todos := loadTodos()
		found := false
		for i, t := range todos {
			if t.ID == id {
				if title != "" {
					todos[i].Title = title
				}
				if desc != "" {
					todos[i].Description = desc
				}
				if status != "" {
					todos[i].Status = status
				}
				found = true
				break
			}
		}
		if !found {
			fmt.Fprintf(cmd.ErrOrStderr(), "Error: todo %s not found\n", id)
			os.Exit(1)
		}
		saveTodos(todos)
		fmt.Println("Todo updated")
	},
}

var todoDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete a todo",
	Run: func(cmd *cobra.Command, args []string) {
		id, _ := cmd.Flags().GetString("id")
		if id == "" {
			fmt.Fprintln(cmd.ErrOrStderr(), "Error: --id is required")
			os.Exit(1)
		}

		todos := loadTodos()
		found := false
		for i, t := range todos {
			if t.ID == id {
				todos = append(todos[:i], todos[i+1:]...)
				found = true
				break
			}
		}
		if !found {
			fmt.Fprintf(cmd.ErrOrStderr(), "Error: todo %s not found\n", id)
			os.Exit(1)
		}
		saveTodos(todos)
		fmt.Println("Todo deleted")
	},
}

func loadTodos() []Todo {
	data, err := os.ReadFile(todoFile)
	if err != nil {
		if os.IsNotExist(err) {
			return []Todo{}
		}
		fmt.Fprintf(os.Stderr, "Error reading todos: %v\n", err)
		return []Todo{}
	}
	var todos []Todo
	if err := json.Unmarshal(data, &todos); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing todos: %v\n", err)
		return []Todo{}
	}
	return todos
}

func saveTodos(todos []Todo) {
	os.MkdirAll(filepath.Dir(todoFile), 0755)
	data, _ := json.MarshalIndent(todos, "", "  ")
	if err := os.WriteFile(todoFile, data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving todos: %v\n", err)
		os.Exit(1)
	}
}

func init() {
	todoCreateCmd.Flags().String("title", "", "Todo title")
	todoCreateCmd.Flags().String("description", "", "Todo description")
	todoCreateCmd.Flags().String("workspace", "", "Workspace ID")

	todoListCmd.Flags().String("status", "", "Filter by status")
	todoListCmd.Flags().String("workspace", "", "Filter by workspace")
	todoListCmd.Flags().Bool("json", false, "JSON output")

	todoUpdateCmd.Flags().String("id", "", "Todo ID")
	todoUpdateCmd.Flags().String("title", "", "New title")
	todoUpdateCmd.Flags().String("description", "", "New description")
	todoUpdateCmd.Flags().String("status", "", "New status")

	todoDeleteCmd.Flags().String("id", "", "Todo ID")

	todoCmd.AddCommand(todoCreateCmd)
	todoCmd.AddCommand(todoListCmd)
	todoCmd.AddCommand(todoUpdateCmd)
	todoCmd.AddCommand(todoDeleteCmd)
}