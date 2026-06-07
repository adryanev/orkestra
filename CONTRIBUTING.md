# Contributing to Orkestra

Thank you for your interest in contributing to Orkestra! This guide will help you get started.

## 🎯 What is Orkestra?

**Orkestra** (from Indonesian *orkestrasi*, orchestration) is a CLI for running Claude Code and Codex in isolated git workspaces while keeping enough state to stop, resume, stream, and inspect those agent sessions from another process.

Use it when a controller such as a developer shell, CI job, MCP client, Hermes, OpenClaw, or other automation needs to launch an agent, follow its output, and come back to the same session later.

## 🚀 Quick Start

### 1. Fork the Repository

```bash
# Click "Fork" on GitHub, then:
git clone https://github.com/YOUR_USERNAME/orkestra.git
cd orkestra
git remote add upstream https://github.com/adryanev/orkestra.git
```

### 2. Set Up Development Environment

```bash
# Install Go 1.26+
# https://go.dev/dl/

# Build the binary
go build -o orkestra .

# Run tests
go test ./...

# Run linter
golangci-lint run
```

### 3. Create a Branch

```bash
git checkout -b feature/your-feature-name
```

## 📋 How to Contribute

### Reporting Bugs

Before creating bug reports, please check existing issues as you might find out that you don't need to create one. When you are creating a bug report, please include as many details as possible:

**Example:**
```markdown
**Describe the bug**
A clear and concise description of what the bug is.

**To Reproduce**
Steps to reproduce the behavior:
1. `orkestra workspace create --repo ...`
2. `orkestra run --workspace ...`
3. See error

**Expected behavior**
A clear and concise description of what you expected to happen.

**Screenshots/Logs**
If applicable, add screenshots or logs to help explain your problem.

**Environment:**
- OS: Linux / macOS / Windows
- Go version: `go version`
- Orkestra version: `orkestra --help`
```

### Suggesting Features

Feature suggestions are always welcome! Please create an issue with:

```markdown
**Is your feature request related to a problem?**
A clear and concise description of what the problem is.

**Describe the solution you'd like**
A clear and concise description of what you want to happen.

**Describe alternatives you've considered**
A clear and concise description of any alternative solutions or features you've considered.

**Additional context**
Add any other context or screenshots about the feature request here.
```

### Pull Requests

1. **Fork** the repository
2. **Create a branch** from `main`
3. **Make your changes**
4. **Write/update tests** if applicable
5. **Ensure all tests pass**: `go test ./...`
6. **Run linter**: `golangci-lint run`
7. **Build successfully**: `go build -o orkestra .`
8. **Commit** with clear messages
9. **Push** to your fork
10. **Open a Pull Request**

### Pull Request Guidelines

**Commit Message Format:**
```
type: subject

body (optional)

footer (optional)
```

**Types:**
- `feat:` A new feature
- `fix:` A bug fix
- `docs:` Documentation only changes
- `style:` Changes that don't affect code logic (formatting, etc.)
- `refactor:` Code change that neither fixes a bug nor adds a feature
- `test:` Adding or updating tests
- `chore:` Changes to build process or auxiliary tools

**Example:**
```
feat: add model selection command

- New `orkestra models` command to list available AI models
- Auto-detect models from Claude CLI and Codex CLI help text
- Fallback to hardcoded list with latest models

Closes #10
```

**PR Checklist:**
- [ ] Code builds successfully
- [ ] All tests pass
- [ ] Linter passes
- [ ] Documentation updated (if applicable)
- [ ] Commit messages follow convention
- [ ] Linked related issues (if any)

## 🧪 Testing

### Running Tests

```bash
# Run all tests
go test ./...

# Run with coverage
go test ./... -coverprofile=coverage.out
go tool cover -html=coverage.out

# Run specific package tests
go test ./pkg/runner/...
```

### Writing Tests

- **Unit tests**: Place in `*_test.go` files alongside the code being tested
- **Integration tests**: Place in `integration_test.go` files
- **Test helpers**: Use `t.Helper()` for test helper functions
- **Table-driven tests**: Prefer table-driven tests for multiple cases

**Example:**
```go
func TestBuildAgentArgsClaudeFresh(t *testing.T) {
    name, args, err := buildAgentArgs(Claude, "do it", "", "", "", "", "")
    if err != nil {
        t.Fatal(err)
    }
    if name != "claude" {
        t.Errorf("name = %q, want claude", name)
    }
    if slices.Contains(args, "--resume") {
        t.Error("fresh run should not contain --resume")
    }
}
```

## 📚 Code Style

### Go Conventions

- Follow [Effective Go](https://go.dev/doc/effective_go)
- Follow [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments)
- Use `gofmt` or `goimports` for formatting
- Keep functions small and focused
- Use meaningful variable names

### Linting

```bash
# Install golangci-lint
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# Run linter
golangci-lint run

# Run with auto-fix
golangci-lint run --fix
```

## 🏗️ Architecture

### Project Structure

```
orkestra/
├── cmd/              # CLI commands (run, resume, workspace, models, etc.)
├── pkg/
│   ├── env/         # Shell environment capture
│   ├── mcp/         # MCP server implementation
│   ├── process/     # Process management
│   ├── runner/      # Agent execution (Claude, Codex)
│   ├── state/       # State persistence
│   └── workspace/   # Workspace management
├── main.go          # Entry point
└── README.md        # Documentation
```

### Key Concepts

1. **Workspaces**: Isolated git worktrees with persistent state
2. **Agents**: Claude Code or Codex CLI running in workspaces
3. **Sessions**: Stored session/thread IDs for resume capability
4. **MCP**: Model Context Protocol server for LSP and workspace tools

## 🔧 Development Workflow

### 1. Local Development

```bash
# Build and test locally
go build -o orkestra .
./orkestra --help

# Test with your local repos
./orkestra workspace create --repo ~/code/your-project --name test
./orkestra run --workspace <id> --prompt "Test feature"
```

### 2. Cross-Platform Builds

```bash
# Build for all platforms (CI does this automatically)
GOOS=linux GOARCH=amd64 go build -o orkestra-linux-amd64 .
GOOS=darwin GOARCH=arm64 go build -o orkestra-darwin-arm64 .
GOOS=windows GOARCH=amd64 go build -o orkestra-windows-amd64.exe .
```

### 3. Release Process

Releases are automated via GitHub Actions:

```bash
# Create a tag
git tag v1.2.0
git push origin v1.2.0

# CI will:
# 1. Build binaries for all platforms
# 2. Create GitHub Release
# 3. Upload binaries as assets
```

## 📖 Documentation

### Updating README

- Keep examples up-to-date with latest features
- Include installation instructions
- Add troubleshooting section for common issues
- Use clear, concise language

### Code Comments

- Exported functions should have godoc comments
- Complex logic should have inline comments
- Avoid obvious comments (let code speak for itself)

**Example:**
```go
// buildAgentArgs builds the command-line arguments for running an agent.
// It handles both Claude Code and Codex, with support for resume sessions,
// model selection, and effort levels.
//
// Parameters:
//   - agent: The agent type (Claude or Codex)
//   - prompt: The user's prompt text
//   - resumeSessionID: Claude session ID for resume (empty for fresh runs)
//   - resumeThreadID: Codex thread ID for resume (empty for fresh runs)
//   - mcpConfigPath: Path to MCP config file (empty for no MCP)
//   - model: AI model to use (empty for default)
//   - effort: Thinking effort level (Claude only, empty for default)
//
// Returns the agent binary name, argument list, and any error.
func buildAgentArgs(agent AgentType, prompt, resumeSessionID, resumeThreadID, mcpConfigPath, model, effort string) (string, []string, error) {
    // ...
}
```

## 🤝 Community

### Communication

- **GitHub Issues**: Bug reports, feature requests
- **GitHub Discussions**: Questions, ideas, show-and-tell
- **PR Comments**: Code review discussions

### Code of Conduct

- Be respectful and inclusive
- Focus on constructive feedback
- Help others learn and grow
- Keep discussions on-topic

## 🎉 Getting Help

- **Documentation**: [README.md](https://github.com/adryanev/orkestra#readme)
- **Examples**: Check existing issues and PRs
- **Hermes Integration**: Load `orkestra-integration` skill for workflow guidance

## 📝 License

By contributing to Orkestra, you agree that your contributions will be licensed under the project's license.

---

**Thank you for contributing to Orkestra!** 🎉

Your contributions help make AI agent orchestration more accessible and powerful for everyone.
