package cmd

import (
	"path/filepath"
	"testing"
)

func TestTodoFileHonorsXorkestraHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XORKESTRA_HOME", dir)
	want := filepath.Join(dir, "todos.json")
	if got := todoFile(); got != want {
		t.Errorf("todoFile() = %q, want %q", got, want)
	}
}

func TestTodoCRUD(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XORKESTRA_HOME", dir)

	// Empty to start.
	if got := loadTodos(); len(got) != 0 {
		t.Fatalf("expected no todos, got %d", len(got))
	}

	// Create.
	if err := mutateTodos(func(ts []Todo) ([]Todo, error) {
		return append(ts, Todo{ID: "abc", Title: "first", Status: "todo"}), nil
	}); err != nil {
		t.Fatal(err)
	}
	todos := loadTodos()
	if len(todos) != 1 || todos[0].Title != "first" {
		t.Fatalf("create failed: %+v", todos)
	}

	// Update.
	if err := mutateTodos(func(ts []Todo) ([]Todo, error) {
		ts[0].Status = "done"
		return ts, nil
	}); err != nil {
		t.Fatal(err)
	}
	if loadTodos()[0].Status != "done" {
		t.Error("update did not persist")
	}

	// Delete.
	if err := mutateTodos(func(ts []Todo) ([]Todo, error) {
		return ts[:0], nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(loadTodos()) != 0 {
		t.Error("delete did not persist")
	}
}

func TestMutateTodosErrorPropagates(t *testing.T) {
	t.Setenv("XORKESTRA_HOME", t.TempDir())
	err := mutateTodos(func(ts []Todo) ([]Todo, error) {
		return nil, errTest
	})
	if err == nil {
		t.Error("expected error to propagate, got nil")
	}
}

var errTest = errString("boom")

type errString string

func (e errString) Error() string { return string(e) }
