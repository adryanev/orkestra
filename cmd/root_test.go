package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adryanev/orkestra/pkg/mcp"
	"github.com/spf13/viper"
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

func TestInitConfigLoadsLspOverridesIntoGlobalViper(t *testing.T) {
	cfg := t.TempDir()
	configPath := filepath.Join(cfg, "orkestra.yaml")
	if err := os.WriteFile(configPath, []byte(`
lsp_servers:
  - server_id: custom-go
    command: custom-gopls
    extensions: ["go"]
    language_id: go
`), 0o644); err != nil {
		t.Fatal(err)
	}

	oldCfgFile := cfgFile
	oldConfigDir := configDir
	cfgFile = configPath
	t.Cleanup(func() {
		cfgFile = oldCfgFile
		configDir = oldConfigDir
		viper.Reset()
	})
	viper.Reset()

	initConfig()

	var overrides []mcp.LspServerConfig
	if err := viper.UnmarshalKey("lsp_servers", &overrides); err != nil {
		t.Fatal(err)
	}
	if len(overrides) != 1 {
		t.Fatalf("overrides = %d, want 1", len(overrides))
	}
	if overrides[0].ServerID != "custom-go" || overrides[0].Command != "custom-gopls" {
		t.Fatalf("override did not load from global viper: %+v", overrides[0])
	}
}
