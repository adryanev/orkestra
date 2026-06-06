package process

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/google/uuid"
)

type ProcessManager struct {
	processes map[string]*ManagedProcess
	sync.Mutex
}

type ManagedProcess struct {
	ID        string
	Cmd       *exec.Cmd
	Stdin     io.WriteCloser
	Stdout    *bufio.Scanner
	Stderr    *bufio.Scanner
	Status    string
	StartTime time.Time
	mu        sync.Mutex // Mutex for process-specific fields like Status
}

func NewProcessManager() *ProcessManager {
	return &ProcessManager{
		processes: make(map[string]*ManagedProcess),
	}
}

func (pm *ProcessManager) StartProcess(command string, args []string, workdir string) (*ManagedProcess, error) {
	pm.Lock()
	defer pm.Unlock()

	processID := uuid.New().String()
	cmd := exec.Command(command, args...)
	cmd.Dir = workdir

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start process %s: %w", command, err)
	}

	managedProc := &ManagedProcess{
		ID:        processID,
		Cmd:       cmd,
		Stdin:     stdinPipe,
		Stdout:    bufio.NewScanner(stdoutPipe),
		Stderr:    bufio.NewScanner(stderrPipe),
		Status:    "running",
		StartTime: time.Now(),
	}

	pm.processes[processID] = managedProc

	go managedProc.monitorOutput()

	return managedProc, nil
}

func (mp *ManagedProcess) monitorOutput() {
	stdoutDone := make(chan struct{})
	stderrDone := make(chan struct{})

	go func() {
		for mp.Stdout.Scan() {
			fmt.Printf("[%s STDOUT]: %s\n", mp.ID, mp.Stdout.Text())
		}
		close(stdoutDone)
	}()

	go func() {
		for mp.Stderr.Scan() {
			fmt.Printf("[%s STDERR]: %s\n", mp.ID, mp.Stderr.Text())
		}
		close(stderrDone)
	}()

	<-stdoutDone
	<-stderrDone

	err := mp.Cmd.Wait()
	mp.mu.Lock()
	if err != nil {
		mp.Status = "error"
		fmt.Printf("Process %s finished with error: %v\n", mp.ID, err)
	} else {
		mp.Status = "exited"
		fmt.Printf("Process %s finished successfully\n", mp.ID)
	}
	mp.mu.Unlock()
}

func (pm *ProcessManager) GetProcess(id string) (*ManagedProcess, error) {
	pm.Lock()
	defer pm.Unlock()

	proc, ok := pm.processes[id]
	if !ok {
		return nil, fmt.Errorf("process with ID %s not found", id)
	}
	return proc, nil
}

func (pm *ProcessManager) KillProcess(id string) error {
	pm.Lock()
	defer pm.Unlock()

	proc, ok := pm.processes[id]
	if !ok {
		return fmt.Errorf("process with ID %s not found", id)
	}

	proc.mu.Lock()
	defer proc.mu.Unlock()

	if err := proc.Cmd.Process.Kill(); err != nil {
		return fmt.Errorf("failed to kill process %s: %w", id, err)
	}
	proc.Status = "killed"
	fmt.Printf("Process %s killed\n", id)
	return nil
}

func (pm *ProcessManager) WriteToProcess(id string, data []byte) error {
	pm.Lock()
	defer pm.Unlock()

	proc, ok := pm.processes[id]
	if !ok {
		return fmt.Errorf("process with ID %s not found", id)
	}

	proc.mu.Lock()
	defer proc.mu.Unlock()

	if proc.Status != "running" {
		return fmt.Errorf("process %s is not running, status: %s", id, proc.Status)
	}

	if _, err := proc.Stdin.Write(data);
		err != nil {
		return fmt.Errorf("failed to write to process %s stdin: %w", id, err)
	}
	return nil
}

func (pm *ProcessManager) SubmitToProcess(id string, data string) error {
	// Write data followed by a newline
	return pm.WriteToProcess(id, append([]byte(data), '\n'))
}

func (pm *ProcessManager) CloseProcessStdin(id string) error {
	pm.Lock()
	defer pm.Unlock()

	proc, ok := pm.processes[id]
	if !ok {
		return fmt.Errorf("process with ID %s not found", id)
	}

	proc.mu.Lock()
	defer proc.mu.Unlock()

	if err := proc.Stdin.Close(); err != nil {
		return fmt.Errorf("failed to close stdin for process %s: %w", id, err)
	}
	return nil
}
