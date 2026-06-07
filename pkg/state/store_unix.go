//go:build unix
// +build unix

// Package state provides cross-process-safe persistence helpers for orkestra's
// JSON state files. orkestra runs as a series of separate, often parallel, CLI
// processes, so an in-process mutex is not enough — writes are serialized by an
// advisory file lock and made atomic with a temp-file-plus-rename.
package state

import (
	"os"
	"path/filepath"
	"syscall"
)

// FileLock is an advisory cross-process lock backed by flock(2) on a lock file.
type FileLock struct {
	f *os.File
}

// Acquire takes an exclusive advisory lock on lockPath, creating the file and
// its parent directory if needed. It blocks until the lock is available.
func Acquire(lockPath string) (*FileLock, error) {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &FileLock{f: f}, nil
}

// Release unlocks and closes the lock file. Safe to call once.
func (l *FileLock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	err := l.f.Close()
	l.f = nil
	return err
}

// WriteAtomic writes data to path by writing a temp file in the same directory
// and renaming it into place. On POSIX the rename is atomic, so a concurrent
// reader sees either the old complete file or the new one, never a partial.
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
