package cmd

import (
	"path/filepath"
	"testing"
)

func TestResolveConfigDirHonorsXorkestraHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XORKESTRA_HOME", dir)
	if got := resolveConfigDir(); got != dir {
		t.Errorf("resolveConfigDir() = %q, want %q (XORKESTRA_HOME)", got, dir)
	}
}

func TestResolveConfigDirDefaultsToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XORKESTRA_HOME", "")
	t.Setenv("HOME", home)
	want := filepath.Join(home, ".orkestra")
	if got := resolveConfigDir(); got != want {
		t.Errorf("resolveConfigDir() = %q, want %q", got, want)
	}
}
