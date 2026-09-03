package guard

import (
	"net/http"
	"regexp"
	"strings"
)

// uuidPat is Coolify's resource identifier shape. Deliberately narrow: no
// slashes, no dots, no traversal — a uuid can never widen the matched route.
const uuidPat = `[A-Za-z0-9][A-Za-z0-9_-]{0,63}`

// envNamePat matches an environment name or uuid in a project path.
const envNamePat = `[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}`

// dbEnginePat is the closed set of managed database engines Coolify provisions.
const dbEnginePat = `(postgresql|mysql|mariadb|mongodb|redis|keydb|clickhouse|dragonfly)`

// deniedPaths are literal sub-paths that a {uuid} segment would otherwise
// match. They are server provisioning routes, which R4 puts out of scope; the
// denylist runs before the allowlist so no uuid pattern can reach them.
var deniedPaths = []*regexp.Regexp{
	regexp.MustCompile(`^/servers/(import|hetzner|digitalocean|vultr)(/|$)`),
}

type routeRule struct {
	path    *regexp.Regexp
	methods map[string]struct{}
}

// RoutePolicy is the deny-by-default allowlist of Coolify API routes this MCP
// may ever reach. It is the enforcement point for R1 (zero delete), R3 (no
// system files) and R4 (no privilege escalation): a route that is not listed
// here cannot be called, no matter what a tool asks for.
type RoutePolicy struct {
	rules []routeRule
}

func NewRoutePolicy() *RoutePolicy {
	return &RoutePolicy{rules: allowedRoutes()}
}

func allowedRoutes() []routeRule {
	r := func(pattern string, methods ...string) routeRule {
		return routeRule{
			path:    regexp.MustCompile(`^` + pattern + `$`),
			methods: methodSet(methods...),
		}
	}
	return []routeRule{
		// --- instance ---
		r(`/version`, "GET"),
		r(`/health`, "GET"),

		// --- inventory ---
		r(`/resources`, "GET"),

		// --- servers (read only: R4 forbids provisioning or mutating servers) ---
		r(`/servers`, "GET"),
		r(`/servers/`+uuidPat, "GET"),
		r(`/servers/`+uuidPat+`/resources`, "GET"),
		r(`/servers/`+uuidPat+`/domains`, "GET"),

		// --- projects ---
		r(`/projects`, "GET", "POST"),
		r(`/projects/`+uuidPat, "GET"),
		r(`/projects/`+uuidPat+`/environments`, "GET"),
		r(`/projects/`+uuidPat+`/`+envNamePat, "GET"),

		// --- applications ---
		r(`/applications`, "GET"),
		r(`/applications/(public|private-github-app|dockerfile|dockerimage)`, "POST"),
		r(`/applications/`+uuidPat, "GET", "PATCH"),
		r(`/applications/`+uuidPat+`/logs`, "GET"),
		r(`/applications/`+uuidPat+`/envs`, "GET", "POST", "PATCH"),
		r(`/applications/`+uuidPat+`/envs/bulk`, "PATCH"),
		r(`/applications/`+uuidPat+`/storages`, "GET"),
		r(`/applications/`+uuidPat+`/scheduled-tasks`, "GET"),
		r(`/applications/`+uuidPat+`/scheduled-tasks/`+uuidPat+`/executions`, "GET"),
		r(`/applications/`+uuidPat+`/(start|stop|restart)`, "POST"),

		// --- databases ---
		r(`/databases`, "GET"),
		r(`/databases/`+dbEnginePat, "POST"),
		r(`/databases/`+uuidPat, "GET", "PATCH"),
		r(`/databases/`+uuidPat+`/logs`, "GET"),
		r(`/databases/`+uuidPat+`/envs`, "GET", "POST", "PATCH"),
		r(`/databases/`+uuidPat+`/envs/bulk`, "PATCH"),
		r(`/databases/`+uuidPat+`/storages`, "GET"),
		r(`/databases/`+uuidPat+`/(start|stop|restart)`, "POST"),

		// --- services ---
		r(`/services`, "GET", "POST"),
		r(`/services/`+uuidPat, "GET", "PATCH"),
		r(`/services/`+uuidPat+`/logs`, "GET"),
		r(`/services/`+uuidPat+`/envs`, "GET", "POST", "PATCH"),
		r(`/services/`+uuidPat+`/envs/bulk`, "PATCH"),
		r(`/services/`+uuidPat+`/storages`, "GET"),
		r(`/services/`+uuidPat+`/scheduled-tasks`, "GET"),
		r(`/services/`+uuidPat+`/scheduled-tasks/`+uuidPat+`/executions`, "GET"),
		r(`/services/`+uuidPat+`/(start|stop|restart)`, "POST"),
		r(`/services/`+uuidPat+`/applications`, "GET"),
		r(`/services/`+uuidPat+`/databases`, "GET"),

		// --- deployments ---
		r(`/deploy`, "POST"),
		r(`/deployments`, "GET"),
		r(`/deployments/`+uuidPat, "GET"),
		r(`/deployments/applications/`+uuidPat, "GET"),
		r(`/deployments/`+uuidPat+`/cancel`, "POST"),
	}
}

func methodSet(methods ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(methods))
	for _, v := range methods {
		m[strings.ToUpper(v)] = struct{}{}
	}
	return m
}

// AssertRequest is the last gate before bytes leave this machine. It enforces
// R1 unconditionally, then the route allowlist.
func (p *RoutePolicy) AssertRequest(method, path string) error {
	method = strings.ToUpper(strings.TrimSpace(method))

	// R1 — zero delete. Checked first and independently of the allowlist so it
	// holds even if a route were ever added with DELETE by mistake.
	if method == http.MethodDelete {
		return NewErrorWithRemedy(CodeDeniedDelete,
			"this MCP never issues DELETE against Coolify, for any resource",
			"delete resources by hand in the Coolify UI")
	}

	if path == "" || !strings.HasPrefix(path, "/") {
		return badInput("path must start with %q, got %q", "/", path)
	}
	// Defence in depth against a caller assembling a path from user input.
	if strings.Contains(path, "..") || strings.Contains(path, "//") {
		return NewError(CodeDeniedScope, "path contains traversal or empty segment")
	}

	for _, denied := range deniedPaths {
		if denied.MatchString(path) {
			return NewError(CodeDeniedScope,
				"route "+path+" provisions or administers servers, which is out of scope (R4)")
		}
	}

	for _, rule := range p.rules {
		if !rule.path.MatchString(path) {
			continue
		}
		if _, ok := rule.methods[method]; ok {
			return nil
		}
		return NewError(CodeDeniedScope,
			method+" is not allowed on "+path)
	}
	return NewErrorWithRemedy(CodeDeniedScope,
		"route "+path+" is outside this MCP's allowlist",
		"server, private key, team, token, notification and backup routes are out of scope by design (R3/R4)")
}
