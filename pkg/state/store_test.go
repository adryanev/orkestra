package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestWriteAtomicRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")
	want := []byte(`{"a":1}`)
	if err := WriteAtomic(path, want, 0644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWriteAtomicLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")
	if err := WriteAtomic(path, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("expected only the target file, found %d entries", len(entries))
	}
}

func TestReadFileMissingReturnsNil(t *testing.T) {
	data, err := ReadFile(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("expected nil error for missing file, got %v", err)
	}
	if data != nil {
		t.Errorf("expected nil data, got %q", data)
	}
}

// TestConcurrentReadModifyWrite is the core guarantee: N goroutines each adding
// a distinct key under the lock-spanning read-modify-write pattern must all
// survive — no lost updates.
func TestConcurrentReadModifyWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "m.json")
	lockPath := filepath.Join(dir, ".lock")

	const n = 25
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			lock, err := Acquire(lockPath)
			if err != nil {
				t.Errorf("acquire: %v", err)
				return
			}
			defer lock.Release()

			m := map[string]int{}
			cur, err := ReadFile(path)
			if err != nil {
				t.Errorf("read: %v", err)
				return
			}
			if len(cur) > 0 {
				if err := json.Unmarshal(cur, &m); err != nil {
					t.Errorf("unmarshal: %v", err)
					return
				}
			}
			m[fmt.Sprintf("key-%d", i)] = i
			out, _ := json.Marshal(m)
			if err := WriteAtomic(path, out, 0644); err != nil {
				t.Errorf("write: %v", err)
			}
		}(i)
	}
	wg.Wait()

	final, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]int{}
	if err := json.Unmarshal(final, &m); err != nil {
		t.Fatal(err)
	}
	if len(m) != n {
		t.Errorf("expected %d keys after concurrent RMW, got %d (lost updates)", n, len(m))
	}
}
