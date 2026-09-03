// Package cli runs a closed set of read-only host diagnostics.
//
// There is no shell anywhere in this package: no "sh -c", no pipes, no
// redirection, no string interpolation into a command line. A command is an
// enum value that maps to a fixed argv, and the only caller-supplied values are
// validated against a regexp before they are appended (A9).
package cli

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"coolify-mcp/internal/guard"
)

// Command names exposed to the agent.
const (
	CmdDockerPS        = "docker_ps"
	CmdDockerStats     = "docker_stats"
	CmdDockerInspect   = "docker_inspect"
	CmdDockerLogs      = "docker_logs"
	CmdDockerImages    = "docker_images"
	CmdDockerSystemDF  = "docker_system_df"
	CmdDockerNetworkLS = "docker_network_ls"
	CmdDiskUsage       = "disk_usage"
	CmdMemoryUsage     = "memory_usage"
	CmdLoadAverage     = "load_average"
	CmdCoolifyVersion  = "coolify_version"
)

// containerIDPattern is the only shape a caller-supplied container reference may
// take. It admits no slash, space, quote or shell metacharacter.
var containerIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

const (
	minLines = 1
	maxLines = 5000
)

// spec is one allowlist entry: a fixed argv plus what the caller may add.
type spec struct {
	argv        []string
	needsTarget bool
	needsLines  bool
	// build assembles the final argv from the validated inputs.
	build func(target string, lines int) []string
}

var commands = map[string]spec{
	CmdDockerPS:        {argv: []string{"docker", "ps", "--format", "json"}},
	CmdDockerStats:     {argv: []string{"docker", "stats", "--no-stream", "--format", "json"}},
	CmdDockerImages:    {argv: []string{"docker", "images", "--format", "json"}},
	CmdDockerSystemDF:  {argv: []string{"docker", "system", "df"}},
	CmdDockerNetworkLS: {argv: []string{"docker", "network", "ls"}},
	CmdDiskUsage:       {argv: []string{"df", "-h"}},
	CmdMemoryUsage:     {argv: []string{"free", "-m"}},
	CmdLoadAverage:     {argv: []string{"uptime"}},
	CmdCoolifyVersion:  {argv: []string{"docker", "inspect", "coolify", "--format", "{{.Config.Image}}"}},

	CmdDockerInspect: {
		needsTarget: true,
		build: func(target string, _ int) []string {
			return []string{"docker", "inspect", target}
		},
	},
	CmdDockerLogs: {
		needsTarget: true,
		needsLines:  true,
		build: func(target string, lines int) []string {
			return []string{"docker", "logs", "--tail", strconv.Itoa(lines), target}
		},
	},
}

// Names lists the allowed commands, for the tool schema and error messages.
func Names() []string {
	out := make([]string, 0, len(commands))
	for name := range commands {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Resolve validates a request and returns the argv to execute. It is the only
// way to obtain an argv, so nothing outside the allowlist can ever run.
func Resolve(command, target string, lines int) ([]string, error) {
	name := strings.ToLower(strings.TrimSpace(command))
	s, ok := commands[name]
	if !ok {
		return nil, guard.NewErrorWithRemedy(guard.CodeDeniedCLI,
			"command "+quote(command)+" is not in the CLI allowlist",
			"allowed commands: "+strings.Join(Names(), ", "))
	}

	target = strings.TrimSpace(target)
	if !s.needsTarget {
		if target != "" {
			return nil, guard.NewError(guard.CodeDeniedCLI,
				name+" takes no container argument; drop the target")
		}
		return append([]string(nil), s.argv...), nil
	}

	if target == "" {
		return nil, guard.NewError(guard.CodeBadInput,
			name+" needs a container id or name in target")
	}
	if !containerIDPattern.MatchString(target) {
		return nil, guard.NewErrorWithRemedy(guard.CodeDeniedCLI,
			"target "+quote(target)+" is not a valid container id or name",
			"a target must match ^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$ — no paths, flags or shell characters")
	}

	if s.needsLines {
		if lines == 0 {
			lines = 200
		}
		if lines < minLines || lines > maxLines {
			return nil, guard.NewError(guard.CodeBadInput,
				"lines must be between "+strconv.Itoa(minLines)+" and "+strconv.Itoa(maxLines))
		}
	}
	return s.build(target, lines), nil
}

func quote(s string) string {
	if s == "" {
		return `""`
	}
	return strconv.Quote(s)
}
