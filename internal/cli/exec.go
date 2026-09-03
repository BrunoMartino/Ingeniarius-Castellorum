package cli

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"

	"coolify-mcp/internal/guard"
)

const (
	execTimeout = 20 * time.Second
	maxOutput   = 64 << 10 // 64 KB, per command, stdout and stderr each
)

// Result is what a diagnostic command produced.
type Result struct {
	Command   string   `json:"command"`
	Argv      []string `json:"argv"`
	ExitCode  int      `json:"exit_code"`
	Stdout    string   `json:"stdout"`
	Stderr    string   `json:"stderr,omitempty"`
	Truncated bool     `json:"truncated,omitempty"`
	TimedOut  bool     `json:"timed_out,omitempty"`
}

// Runner executes allowlisted diagnostics. Enabled=false makes every call fail
// closed, which is what COOLIFY_MCP_ALLOW_CLI=false buys.
type Runner struct {
	Enabled bool
	Timeout time.Duration
}

func NewRunner(enabled bool) *Runner {
	return &Runner{Enabled: enabled, Timeout: execTimeout}
}

// Run validates the request through the allowlist and executes the resulting
// argv directly — exec.Command with a fixed program and validated arguments,
// never a shell.
func (r *Runner) Run(ctx context.Context, command, target string, lines int) (*Result, error) {
	if r == nil || !r.Enabled {
		return nil, guard.NewErrorWithRemedy(guard.CodeDeniedCLI,
			"local CLI diagnostics are disabled on this server",
			"set COOLIFY_MCP_ALLOW_CLI=true to enable them")
	}
	argv, err := Resolve(command, target, lines)
	if err != nil {
		return nil, err
	}

	timeout := r.Timeout
	if timeout <= 0 {
		timeout = execTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// An empty environment: nothing here needs inherited credentials, and a
	// diagnostic must not be able to read the process's Coolify token.
	cmd.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}

	runErr := cmd.Run()

	out, outTrunc := truncate(stdout.String())
	errOut, errTrunc := truncate(stderr.String())
	res := &Result{
		Command:   strings.ToLower(strings.TrimSpace(command)),
		Argv:      argv,
		Stdout:    out,
		Stderr:    errOut,
		Truncated: outTrunc || errTrunc,
	}

	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		res.TimedOut = true
		res.ExitCode = -1
		return res, nil
	}
	var exitErr *exec.ExitError
	switch {
	case runErr == nil:
		res.ExitCode = 0
	case errors.As(runErr, &exitErr):
		// A non-zero exit is data, not a tool failure: "no such container" is
		// something the agent should read and act on.
		res.ExitCode = exitErr.ExitCode()
	default:
		return nil, guard.NewError(guard.CodeUpstream,
			"could not run "+argv[0]+": "+runErr.Error())
	}
	return res, nil
}

func truncate(s string) (string, bool) {
	if len(s) <= maxOutput {
		return s, false
	}
	return s[:maxOutput] + "\n…[truncated at 64 KB]", true
}
