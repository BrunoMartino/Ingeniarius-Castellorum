package guard

import (
	"net/http"
	"testing"
)

// R1: DELETE never leaves the machine, whatever route is asked for — including
// routes that are otherwise on the allowlist.
func TestDeleteIsAlwaysDenied(t *testing.T) {
	p := NewRoutePolicy()
	paths := []string{
		"/applications/abc",
		"/databases/abc",
		"/services/abc",
		"/projects/abc",
		"/servers/abc",
		"/security/keys/abc",
		"/applications/abc/envs/env-1",
		"/applications/abc/storages/s1",
		"/deployments/abc",
		"/anything/at/all",
	}
	for _, path := range paths {
		err := p.AssertRequest(http.MethodDelete, path)
		if !IsCode(err, CodeDeniedDelete) {
			t.Errorf("DELETE %s: want DENIED_DELETE, got %v", path, err)
		}
	}
	// Lower case and mixed case must not slip past.
	for _, method := range []string{"delete", "Delete", " DELETE "} {
		if err := p.AssertRequest(method, "/applications/abc"); !IsCode(err, CodeDeniedDelete) {
			t.Errorf("method %q: want DENIED_DELETE, got %v", method, err)
		}
	}
}

// R4: no route that could escalate privilege or reconfigure the instance is
// reachable, by any method.
func TestEscalationRoutesAreOutsideTheAllowlist(t *testing.T) {
	p := NewRoutePolicy()
	forbidden := []string{
		"/security/keys",
		"/security/keys/abc",
		"/teams",
		"/teams/1/members",
		"/team",
		"/team/members",
		"/enable",
		"/disable",
		"/mcp/enable",
		"/mcp/disable",
		"/cloud-tokens",
		"/github-apps",
		"/gitlab-apps",
		"/notifications/email",
		"/s3-storages",
		"/servers/import",
		"/servers/hetzner",
		"/servers/abc/proxy",
		"/servers/abc/validate",
		"/servers/abc/docker-cleanup/run",
		"/databases/abc/backups",
		"/applications/abc/clone",
		"/applications/abc/move",
		"/applications/abc/rollback",
		"/applications/abc/scheduled-tasks/t1/execute",
	}
	for _, path := range forbidden {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodPut} {
			if err := p.AssertRequest(method, path); err == nil {
				t.Errorf("%s %s was allowed and must not be", method, path)
			}
		}
	}
}

func TestAllowlistedRoutes(t *testing.T) {
	p := NewRoutePolicy()
	allowed := []struct{ method, path string }{
		{http.MethodGet, "/resources"},
		{http.MethodGet, "/servers"},
		{http.MethodGet, "/servers/abc/resources"},
		{http.MethodGet, "/projects"},
		{http.MethodPost, "/projects"},
		{http.MethodGet, "/projects/abc/production"},
		{http.MethodGet, "/applications/abc"},
		{http.MethodPatch, "/applications/abc"},
		{http.MethodGet, "/applications/abc/logs"},
		{http.MethodPatch, "/applications/abc/envs/bulk"},
		{http.MethodGet, "/applications/abc/storages"},
		{http.MethodGet, "/applications/abc/scheduled-tasks/t1/executions"},
		{http.MethodPost, "/applications/abc/start"},
		{http.MethodPost, "/applications/abc/stop"},
		{http.MethodPost, "/applications/abc/restart"},
		{http.MethodPost, "/applications/public"},
		{http.MethodPost, "/applications/dockerimage"},
		{http.MethodPost, "/databases/postgresql"},
		{http.MethodPatch, "/databases/abc"},
		{http.MethodPost, "/services"},
		{http.MethodPatch, "/services/abc"},
		{http.MethodPost, "/deploy"},
		{http.MethodGet, "/deployments"},
		{http.MethodGet, "/services/abc/applications/def/logs"},
		{http.MethodGet, "/services/abc/databases/def/logs"},
		{http.MethodPost, "/deployments/abc/cancel"},
	}
	for _, c := range allowed {
		if err := p.AssertRequest(c.method, c.path); err != nil {
			t.Errorf("%s %s: want allowed, got %v", c.method, c.path, err)
		}
	}
}

// A route on the allowlist must not accept a method it was not granted.
func TestMethodIsScopedPerRoute(t *testing.T) {
	p := NewRoutePolicy()
	cases := []struct{ method, path string }{
		{http.MethodPost, "/applications/abc"},   // create is only via the typed endpoints
		{http.MethodPatch, "/servers/abc"},       // R4
		{http.MethodPost, "/servers"},            // R4
		{http.MethodPatch, "/projects/abc"},      // out of scope in v1
		{http.MethodGet, "/applications/public"}, // creation endpoint, POST only
		{http.MethodPut, "/applications/abc"},
		{http.MethodPost, "/services/abc/applications/def/logs"},
		{http.MethodPatch, "/services/abc/applications/def/logs"},
	}
	for _, c := range cases {
		if err := p.AssertRequest(c.method, c.path); !IsCode(err, CodeDeniedScope) {
			t.Errorf("%s %s: want DENIED_SCOPE, got %v", c.method, c.path, err)
		}
	}
}

// A uuid can never widen the matched route: no traversal, no extra segments.
func TestPathInjectionIsRejected(t *testing.T) {
	p := NewRoutePolicy()
	bad := []string{
		"/applications/../security/keys",
		"/applications/abc/../../security/keys",
		"/applications//abc",
		"/applications/abc/envs/../../../teams",
		"applications/abc",
		"",
		"/applications/ab c/logs",
		"/applications/abc%2F..%2Fteams",
	}
	for _, path := range bad {
		if err := p.AssertRequest(http.MethodGet, path); err == nil {
			t.Errorf("GET %q was allowed and must not be", path)
		}
	}
}

// The engine segment of a database creation path is a closed set.
func TestDatabaseEngineSegmentIsClosed(t *testing.T) {
	p := NewRoutePolicy()
	for _, engine := range []string{"postgresql", "mysql", "mariadb", "mongodb", "redis", "keydb", "clickhouse", "dragonfly"} {
		if err := p.AssertRequest(http.MethodPost, "/databases/"+engine); err != nil {
			t.Errorf("POST /databases/%s: want allowed, got %v", engine, err)
		}
	}
	// An arbitrary segment matches /databases/{uuid}, which grants GET and
	// PATCH but never POST.
	if err := p.AssertRequest(http.MethodPost, "/databases/sqlite"); !IsCode(err, CodeDeniedScope) {
		t.Errorf("POST /databases/sqlite: want DENIED_SCOPE, got %v", err)
	}
}
