package cli

import (
	"strings"
	"testing"

	"coolify-mcp/internal/guard"
)

// Everything outside the enum is refused, including the destructive verbs the
// spec names explicitly.
func TestCommandsOutsideTheEnumAreDenied(t *testing.T) {
	forbidden := []string{
		"rm", "rmi", "prune", "docker_rm", "docker_prune", "docker_volume_rm",
		"docker_network_rm", "docker_down", "docker_kill", "docker_stop",
		"docker_restart", "docker_exec", "docker_cp", "systemctl", "sed", "tee",
		"sudo", "cat", "ls", "curl", "sh", "bash",
		"docker ps", "docker_ps; rm -rf /", "DOCKER_PS extra", "",
	}
	for _, cmd := range forbidden {
		if _, err := Resolve(cmd, "", 0); !guard.IsCode(err, guard.CodeDeniedCLI) {
			t.Errorf("Resolve(%q): want DENIED_CLI, got %v", cmd, err)
		}
	}
}

func TestAllowlistedCommandsResolveToFixedArgv(t *testing.T) {
	cases := map[string][]string{
		CmdDockerPS:        {"docker", "ps", "--format", "json"},
		CmdDockerStats:     {"docker", "stats", "--no-stream", "--format", "json"},
		CmdDockerImages:    {"docker", "images", "--format", "json"},
		CmdDockerSystemDF:  {"docker", "system", "df"},
		CmdDockerNetworkLS: {"docker", "network", "ls"},
		CmdDiskUsage:       {"df", "-h"},
		CmdMemoryUsage:     {"free", "-m"},
		CmdLoadAverage:     {"uptime"},
		CmdCoolifyVersion:  {"docker", "inspect", "coolify", "--format", "{{.Config.Image}}"},
	}
	for cmd, want := range cases {
		got, err := Resolve(cmd, "", 0)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", cmd, err)
		}
		if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
			t.Errorf("Resolve(%q) = %v, want %v", cmd, got, want)
		}
	}
}

// No allowlisted argv may contain a shell, a pipe, or a redirection.
func TestNoArgvInvokesAShell(t *testing.T) {
	for _, name := range Names() {
		argv, err := Resolve(name, "container1", 100)
		if err != nil {
			// Commands that take no target are resolved without one.
			argv, err = Resolve(name, "", 0)
		}
		if err != nil {
			t.Fatalf("Resolve(%q): %v", name, err)
		}
		switch argv[0] {
		case "docker", "df", "free", "uptime":
		default:
			t.Errorf("%s runs %q, which is not one of the four allowed programs", name, argv[0])
		}
		for _, arg := range argv {
			for _, meta := range []string{"|", ";", "&", ">", "<", "$(", "`", "&&"} {
				if strings.Contains(arg, meta) {
					t.Errorf("%s argv contains shell metacharacter %q in %q", name, meta, arg)
				}
			}
		}
	}
}

func TestTargetMustMatchThePattern(t *testing.T) {
	bad := []string{
		"../etc/passwd",
		"/data/coolify/.env",
		"~/.ssh/id_rsa",
		"a b",
		"a;rm -rf /",
		"a|b",
		"a$(id)",
		"-rf",
		"--format={{.}}",
		"'",
		strings.Repeat("a", 129),
	}
	for _, target := range bad {
		if _, err := Resolve(CmdDockerInspect, target, 0); !guard.IsCode(err, guard.CodeDeniedCLI) {
			t.Errorf("Resolve(docker_inspect, %q): want DENIED_CLI, got %v", target, err)
		}
	}
	for _, target := range []string{"abc", "my-app_1.2", "0a1b2c3d4e5f", strings.Repeat("a", 128)} {
		if _, err := Resolve(CmdDockerInspect, target, 0); err != nil {
			t.Errorf("Resolve(docker_inspect, %q): want allowed, got %v", target, err)
		}
	}
}

func TestLinesBounds(t *testing.T) {
	for _, lines := range []int{-1, minLines - 1, maxLines + 1, 100000} {
		if lines == 0 {
			continue
		}
		if _, err := Resolve(CmdDockerLogs, "abc", lines); !guard.IsCode(err, guard.CodeBadInput) {
			t.Errorf("lines=%d: want BAD_INPUT, got %v", lines, err)
		}
	}
	argv, err := Resolve(CmdDockerLogs, "abc", 0)
	if err != nil {
		t.Fatalf("default lines: %v", err)
	}
	if strings.Join(argv, " ") != "docker logs --tail 200 abc" {
		t.Errorf("default argv = %v", argv)
	}
	for _, lines := range []int{minLines, 200, maxLines} {
		if _, err := Resolve(CmdDockerLogs, "abc", lines); err != nil {
			t.Errorf("lines=%d: want allowed, got %v", lines, err)
		}
	}
}

// A target passed to a command that takes none must not be silently ignored.
func TestUnexpectedTargetIsRejected(t *testing.T) {
	if _, err := Resolve(CmdDockerPS, "abc", 0); !guard.IsCode(err, guard.CodeDeniedCLI) {
		t.Errorf("docker_ps with a target: want DENIED_CLI, got %v", err)
	}
}

func TestDisabledRunnerRefusesEverything(t *testing.T) {
	r := NewRunner(false)
	for _, name := range Names() {
		if _, err := r.Run(t.Context(), name, "abc", 10); !guard.IsCode(err, guard.CodeDeniedCLI) {
			t.Errorf("disabled runner, %s: want DENIED_CLI, got %v", name, err)
		}
	}
}
