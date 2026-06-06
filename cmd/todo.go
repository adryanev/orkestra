package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/adryanev/orkestra/pkg/state"
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

// todoFile resolves the todos path under the active config dir at call time,
// so XORKESTRA_HOME and --config are honored (init() runs before they resolve).
func todoFile() string {
	return filepath.Join(getConfigDir(), "todos.json")
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

		todo := Todo{
			ID:          uuid.New().String(),
			Title:       title,
			Description: desc,
			Status:      "todo",
			CreatedAt:   time.Now().UTC().Format(time.RFC3339),
			WorkspaceID: ws,
		}
		if err := mutateTodos(func(todos []Todo) ([]Todo, error) {
			return append(todos, todo), nil
		}); err != nil {
			emitError(err)
		}

		emitResult(
			fmt.Sprintf("Created todo %s: %s", todo.ID[:8], todo.Title),
			todo,
		)
	},
}

var todoListCmd = &cobra.Command{
	Use:   "list",
	Short: "List todos",
	Run: func(cmd *cobra.Command, args []string) {
		statusFilter, _ := cmd.Flags().GetString("status")
		wsFilter, _ := cmd.Flags().GetString("workspace")

		todos := loadTodos()
		filtered := []Todo{}
		for _, t := range todos {
			if statusFilter != "" && t.Status != statusFilter {
				continue
			}
			if wsFilter != "" && t.WorkspaceID != wsFilter {
				continue
			}
			filtered = append(filtered, t)
		}

		if jsonOutput {
			emitResult("", filtered)
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
			emitError(fmt.Errorf("--id is required"))
		}

		if err := mutateTodos(func(todos []Todo) ([]Todo, error) {
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
					return todos, nil
				}
			}
			return nil, fmt.Errorf("todo %s not found", id)
		}); err != nil {
			emitError(err)
		}
		emitResult("Todo updated", map[string]string{"id": id, "status": "updated"})
	},
}

var todoDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete a todo",
	Run: func(cmd *cobra.Command, args []string) {
		id, _ := cmd.Flags().GetString("id")
		if id == "" {
			emitError(fmt.Errorf("--id is required"))
		}

		if err := mutateTodos(func(todos []Todo) ([]Todo, error) {
			for i, t := range todos {
				if t.ID == id {
					return append(todos[:i], todos[i+1:]...), nil
				}
			}
			return nil, fmt.Errorf("todo %s not found", id)
		}); err != nil {
			emitError(err)
		}
		emitResult("Todo deleted", map[string]string{"id": id, "status": "deleted"})
	},
}

func loadTodos() []Todo {
	data, err := state.ReadFile(todoFile())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading todos: %v\n", err)
		return []Todo{}
	}
	if len(data) == 0 {
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
	data, _ := json.MarshalIndent(todos, "", "  ")
	if err := state.WriteAtomic(todoFile(), data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving todos: %v\n", err)
		os.Exit(1)
	}
}

// mutateTodos performs a lock-spanning read-modify-write so concurrent todo
// commands cannot clobber each other (same guarantee as the workspace store).
func mutateTodos(apply func([]Todo) ([]Todo, error)) error {
	lock, err := state.Acquire(filepath.Join(getConfigDir(), ".state.lock"))
	if err != nil {
		return err
	}
	defer lock.Release()

	todos := loadTodos()
	updated, err := apply(todos)
	if err != nil {
		return err
	}
	saveTodos(updated)
	return nil
}

func init() {
	todoCreateCmd.Flags().String("title", "", "Todo title")
	todoCreateCmd.Flags().String("description", "", "Todo description")
	todoCreateCmd.Flags().String("workspace", "", "Workspace ID")

	todoListCmd.Flags().String("status", "", "Filter by status")
	todoListCmd.Flags().String("workspace", "", "Filter by workspace")

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
