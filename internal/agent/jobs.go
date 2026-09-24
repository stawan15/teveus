package agent

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
)

// Background commands: Bash with run_in_background starts one and returns at
// once; the model reads its output with BashOutput and stops it with
// KillShell. They belong to the session, not the turn, so a dev server
// survives an interrupt, and all of them stop when the session closes.

const jobKeep = 1 << 20 // output kept per job; older output is dropped

type job struct {
	id, command string
	cancel      context.CancelFunc

	mu      sync.Mutex
	buf     []byte
	dropped int // bytes dropped from the front of buf
	read    int // bytes (counted from the start of the output) already returned
	done    bool
	killed  bool
	err     error
}

// Write collects the command's output; the shell's stdout and stderr share it.
func (j *job) Write(p []byte) (int, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.buf = append(j.buf, p...)
	if extra := len(j.buf) - jobKeep; extra > 0 {
		j.buf = append([]byte(nil), j.buf[extra:]...)
		j.dropped += extra
	}
	return len(p), nil
}

func (t *Toolbox) startJob(command string) string {
	ctx, cancel := context.WithCancel(context.Background())
	cmd := shellCommand(ctx, command)
	cmd.Dir = t.Cwd
	t.jobMu.Lock()
	t.jobSeq++
	j := &job{id: fmt.Sprintf("bash_%d", t.jobSeq), command: command, cancel: cancel}
	if t.jobs == nil {
		t.jobs = map[string]*job{}
	}
	t.jobs[j.id] = j
	t.jobMu.Unlock()

	cmd.Stdout, cmd.Stderr = j, j
	if err := cmd.Start(); err != nil {
		j.finish(err)
		return j.id
	}
	go func() { j.finish(cmd.Wait()) }()
	return j.id
}

func (j *job) finish(err error) {
	j.mu.Lock()
	j.done, j.err = true, err
	j.mu.Unlock()
	j.cancel()
}

func (t *Toolbox) job(id string) (*job, error) {
	t.jobMu.Lock()
	defer t.jobMu.Unlock()
	if j, ok := t.jobs[id]; ok {
		return j, nil
	}
	return nil, fmt.Errorf("no background command %q", id)
}

func runBashOutput(_ context.Context, t *Toolbox, in map[string]any) (string, error) {
	j, err := t.job(str(in, "bash_id"))
	if err != nil {
		return "", err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	start := max(j.read-j.dropped, 0)
	out := string(j.buf[start:])
	skipped := max(j.dropped-j.read, 0)
	j.read = j.dropped + len(j.buf)
	status := "running"
	if j.done {
		status = "finished"
		if j.killed {
			status = "stopped"
		} else if ee, ok := j.err.(*exec.ExitError); ok {
			status = fmt.Sprintf("exited with code %d", ee.ExitCode())
		} else if j.err != nil {
			status = "stopped: " + j.err.Error()
		}
	}
	res := fmt.Sprintf("[%s: %s, %s]\n", j.id, j.command, status)
	if skipped > 0 {
		res += fmt.Sprintf("… %d bytes of older output were dropped …\n", skipped)
	}
	if out == "" {
		out = "(no new output)"
	}
	return res + capOutput(out), nil
}

func runKillShell(_ context.Context, t *Toolbox, in map[string]any) (string, error) {
	j, err := t.job(str(in, "shell_id"))
	if err != nil {
		return "", err
	}
	j.mu.Lock()
	j.killed = true
	j.mu.Unlock()
	j.cancel()
	return "Stopped " + j.id + " (" + j.command + ").", nil
}

// closeJobs stops every background command.
func (t *Toolbox) closeJobs() {
	t.jobMu.Lock()
	defer t.jobMu.Unlock()
	for _, j := range t.jobs {
		j.cancel()
	}
}
