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
	if got, err := loadTodos(); err != nil || len(got) != 0 {
		t.Fatalf("expected no todos, got %d (err=%v)", len(got), err)
	}

	// Create.
	if err := mutateTodos(func(ts []Todo) ([]Todo, error) {
		return append(ts, Todo{ID: "abc", Title: "first", Status: "todo"}), nil
	}); err != nil {
		t.Fatal(err)
	}
	todos, err := loadTodos()
	if err != nil {
		t.Fatal(err)
	}
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
	if got, err := loadTodos(); err != nil || got[0].Status != "done" {
		t.Errorf("update did not persist (err=%v)", err)
	}

	// Delete.
	if err := mutateTodos(func(ts []Todo) ([]Todo, error) {
		return ts[:0], nil
	}); err != nil {
		t.Fatal(err)
	}
	if got, err := loadTodos(); err != nil || len(got) != 0 {
		t.Errorf("delete did not persist (err=%v)", err)
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
