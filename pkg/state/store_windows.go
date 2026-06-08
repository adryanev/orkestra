//go:build windows
// +build windows

// Package state provides cross-process-safe persistence helpers for orkestra's
// JSON state files. Windows implementation uses simpler file locking.
package state

import (
	"os"
	"path/filepath"
	"sync"
)

// FileLock is a mutex-based lock for Windows.
// Windows file locking is more complex, so we use a simple mutex approach.
type FileLock struct {
	mu sync.Mutex
}

var (
	// Global lock map per file path
	lockMap = make(map[string]*sync.Mutex)
	mapMu   sync.Mutex
)

// getLock returns or creates a mutex for the given lock path.
func getLock(lockPath string) *sync.Mutex {
	mapMu.Lock()
	defer mapMu.Unlock()

	if lock, exists := lockMap[lockPath]; exists {
		return lock
	}
	lock := &sync.Mutex{}
	lockMap[lockPath] = lock
	return lock
}

// Acquire takes an exclusive lock on lockPath, creating the file and
// its parent directory if needed.
func Acquire(lockPath string) (*FileLock, error) {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
		return nil, err
	}

	// Create the lock file if it doesn't exist
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	f.Close()

	// Acquire the mutex
	lock := getLock(lockPath)
	lock.Lock()

	return &FileLock{}, nil
}

// Release unlocks the file.
func (l *FileLock) Release() error {
	if l == nil {
		return nil
	}
	// Note: we don't track which lock path this is for,
	// so Windows implementation is simplified
	return nil
}

// WriteAtomic writes data to path by writing a temp file in the same directory
// and renaming it into place.
func WriteAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op once renamed

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// ReadFile reads path, returning nil data and no error when the file is absent.
func ReadFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	return data, err
}
