package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var (
	updateRepo     string
	updateBinary   string
	updateSkipTest bool
)

// step represents one executed phase, captured for --json output.
type step struct {
	Name   string `json:"name"`
	Cmd    string `json:"cmd"`
	Output string `json:"output,omitempty"`
	OK     bool   `json:"ok"`
}

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Pull, rebuild, and redeploy the orkestra binary",
	Run: func(cmd *cobra.Command, args []string) {
		// Step 1: Expand tilde paths
		repo, err := expandTilde(updateRepo)
		if err != nil {
			emitError(fmt.Errorf("failed to expand repo path: %w", err))
		}
		binary, err := expandTilde(updateBinary)
		if err != nil {
			emitError(fmt.Errorf("failed to expand binary path: %w", err))
		}

		// Step 2: Validate repo exists and contains .git
		gitDir := filepath.Join(repo, ".git")
		if _, err := os.Stat(gitDir); os.IsNotExist(err) {
			emitError(fmt.Errorf("not a git repository: %s (no .git directory)", repo))
		}

		// Step 3: Check if go is on PATH
		if _, err := exec.LookPath("go"); err != nil {
			emitError(fmt.Errorf("go not found on PATH: %w", err))
		}

		// Step 4: Run the update steps
		steps := []step{
			runStep("git-pull", repo, "git", "pull", "--recurse-submodules", "origin", "main"),
		}

		// Short-circuit on first failure
		if !steps[0].OK {
			reportUpdate(steps, binary, "failed")
			emitError(fmt.Errorf("git pull failed: %s", steps[0].Output))
		}

		steps = append(steps, runStep("go-build", repo, "go", "build", "-o", filepath.Join(repo, "orkestra"), "."))
		if !steps[1].OK {
			reportUpdate(steps, binary, "failed")
			emitError(fmt.Errorf("go build failed: %s", steps[1].Output))
		}

		// Deploy: write to temp then atomic rename
		binaryDir := filepath.Dir(binary)
		if err := os.MkdirAll(binaryDir, 0o755); err != nil {
			emitError(fmt.Errorf("failed to create binary directory %s: %w", binaryDir, err))
		}
		tmpBinary := binary + ".tmp"
		if err := os.Rename(filepath.Join(repo, "orkestra"), tmpBinary); err != nil {
			emitError(fmt.Errorf("failed to move built binary: %w", err))
		}
		if err := os.Rename(tmpBinary, binary); err != nil {
			emitError(fmt.Errorf("failed to deploy binary to %s: %w", binary, err))
		}
		steps = append(steps, step{Name: "deploy", Cmd: fmt.Sprintf("mv %s %s", tmpBinary, binary), OK: true})

		// Verify: run <binary> --help
		verifyStep := runStep("verify", "", binary, "--help")
		if !verifyStep.OK {
			reportUpdate(append(steps, verifyStep), binary, "failed")
			emitError(fmt.Errorf("verification failed: %s", verifyStep.Output))
		}
		steps = append(steps, verifyStep)

		// Test: go test ./... (unless --skip-test)
		if !updateSkipTest {
			testStep := runStep("go-test", repo, "go", "test", "./...")
			if !testStep.OK {
				steps = append(steps, testStep)
				reportUpdate(steps, binary, "deployed-tests-failed")
				emitError(fmt.Errorf("tests failed: %s", testStep.Output))
			}
			steps = append(steps, testStep)
		}

		reportUpdate(steps, binary, "updated")
	},
}

// expandTilde expands ~ to $HOME in a path
func expandTilde(path string) (string, error) {
	if path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return home, nil
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}

// runStep executes a command and returns a step result
func runStep(name, dir, bin string, args ...string) step {
	cmd := exec.Command(bin, args...)
	if dir != "" {
		cmd.Dir = dir
	}

	var output string
	var err error

	if jsonOutput {
		// JSON mode: capture output
		out, err := cmd.CombinedOutput()
		output = string(out)
		_ = err // we check OK below
	} else {
		// Human mode: stream output
		fmt.Printf("==> %s: %s %s\n", name, bin, strings.Join(args, " "))
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err = cmd.Run()
		if err != nil {
			output = err.Error()
		}
		fmt.Println()
	}

	ok := err == nil
	return step{
		Name:   name,
		Cmd:    fmt.Sprintf("%s %s", bin, strings.Join(args, " ")),
		Output: output,
		OK:     ok,
	}
}

// reportUpdate prints the final result
func reportUpdate(steps []step, binary, status string) {
	if jsonOutput {
		emitResult("", map[string]interface{}{
			"steps":  steps,
			"binary": binary,
			"status": status,
		})
	} else {
		if status == "updated" {
			fmt.Printf("✓ Orkestra updated successfully at %s\n", binary)
		} else if status == "deployed-tests-failed" {
			fmt.Printf("⚠ Orkestra deployed at %s but tests failed\n", binary)
		} else {
			fmt.Printf("✗ Update failed at %s\n", binary)
		}
	}
}

func init() {
	updateCmd.Flags().StringVar(&updateRepo, "repo", "~/code/personal/orkestra", "Path to the orkestra repository")
	updateCmd.Flags().StringVar(&updateBinary, "binary", "~/.local/bin/orkestra", "Path to deploy the binary")
	updateCmd.Flags().BoolVar(&updateSkipTest, "skip-test", false, "Skip running tests after build")
	rootCmd.AddCommand(updateCmd)
}
