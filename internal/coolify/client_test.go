package coolify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"coolify-mcp/internal/guard"
)

// deleteTrap fails the test if any request reaches it with DELETE.
func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *guard.Auditor, *int32) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if r.Method == http.MethodDelete {
			t.Errorf("a DELETE reached the network: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q", got)
		}
		handler(w, r)
	}))
	t.Cleanup(srv.Close)

	audit := guard.NewAuditor(t.TempDir() + "/audit.jsonl")
	c := NewClient(srv.URL, "test-token", "tester", guard.NewRoutePolicy(), audit, srv.Client())
	return c, audit, &hits
}

// R1 at the transport: even calling the client directly, DELETE never goes out.
func TestClientRefusesDeleteBeforeTheNetwork(t *testing.T) {
	c, audit, hits := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{}`))
	})
	err := c.do(context.Background(), http.MethodDelete, "/applications/abc", nil, nil, nil)
	if !guard.IsCode(err, guard.CodeDeniedDelete) {
		t.Fatalf("want DENIED_DELETE, got %v", err)
	}
	if atomic.LoadInt32(hits) != 0 {
		t.Fatal("the request left the machine")
	}
	// A8: the refusal itself is auditable.
	assertAuditContains(t, audit, `"result":"denied"`, `"code":"DENIED_DELETE"`)
}

func TestClientRefusesRoutesOutsideTheAllowlist(t *testing.T) {
	c, _, hits := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) })
	for _, path := range []string{"/security/keys", "/teams", "/enable", "/servers/abc/proxy"} {
		if err := c.Get(context.Background(), path, nil, nil); !guard.IsCode(err, guard.CodeDeniedScope) {
			t.Errorf("GET %s: want DENIED_SCOPE, got %v", path, err)
		}
	}
	if atomic.LoadInt32(hits) != 0 {
		t.Fatal("a denied route still reached the network")
	}
}

func TestUpstreamErrorsAreTyped(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/missing") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"message":"boom"}`))
	})
	if err := c.Get(context.Background(), "/applications/missing", nil, nil); !guard.IsCode(err, guard.CodeNotFound) {
		t.Errorf("want NOT_FOUND, got %v", err)
	}
	err := c.Get(context.Background(), "/applications/other", nil, nil)
	if !guard.IsCode(err, guard.CodeUpstream) {
		t.Errorf("want UPSTREAM_ERROR, got %v", err)
	}
}

// The token lives in a header and must never appear in an error message.
func TestErrorsDoNotLeakTheToken(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"message":"bad"}`))
	})
	err := c.Get(context.Background(), "/applications/abc", nil, nil)
	if err == nil || strings.Contains(err.Error(), "test-token") {
		t.Fatalf("error leaked the token: %v", err)
	}
}

func TestInventoryClassifiesKindsAndStates(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[
			{"uuid":"a1","name":"api","type":"application","status":"running:healthy"},
			{"uuid":"d1","name":"pg","type":"standalone-postgresql","status":"exited"},
			{"uuid":"s1","name":"plausible","type":"service","status":"degraded"},
			{"uuid":"x1","name":"odd","type":"application","status":"who-knows"},
			{"uuid":"","name":"junk","type":"application"},
			{"uuid":"z1","name":"unmapped","type":"something-else"}
		]`))
	})
	items, err := c.Inventory(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 4 {
		t.Fatalf("got %d resources, want 4 (rows without a uuid or a known type are dropped): %+v", len(items), items)
	}
	byUUID := map[string]Resource{}
	for _, i := range items {
		byUUID[i.UUID] = i
	}
	if byUUID["a1"].Kind != KindApplication || byUUID["a1"].State != "active" {
		t.Errorf("a1 = %+v", byUUID["a1"])
	}
	if byUUID["d1"].Kind != KindDatabase || byUUID["d1"].State != "inactive" {
		t.Errorf("d1 = %+v", byUUID["d1"])
	}
	if byUUID["s1"].Kind != KindService || byUUID["s1"].State != "active" {
		t.Errorf("s1 = %+v", byUUID["s1"])
	}
	if byUUID["x1"].State != "unknown" {
		t.Errorf("x1 = %+v", byUUID["x1"])
	}
}

func TestResolveProbesTypedEndpointsWhenNotInInventory(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/resources":
			w.Write([]byte(`[]`))
		case "/api/v1/applications/new1":
			w.WriteHeader(http.StatusNotFound)
		case "/api/v1/databases/new1":
			w.WriteHeader(http.StatusNotFound)
		case "/api/v1/services/new1":
			w.Write([]byte(`{"uuid":"new1","name":"fresh","status":"stopped"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	res, err := c.Resolve(context.Background(), "new1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != KindService || res.Status != "stopped" {
		t.Fatalf("resolved to %+v", res)
	}
	if _, err := c.Resolve(context.Background(), "nope"); !guard.IsCode(err, guard.CodeNotFound) {
		t.Fatalf("want NOT_FOUND, got %v", err)
	}
}

func TestMaskValue(t *testing.T) {
	if got := MaskValue(""); got != "" {
		t.Errorf("empty stays empty, got %q", got)
	}
	got := MaskValue("hunter2")
	if strings.Contains(got, "hunter2") {
		t.Fatalf("mask leaked the value: %q", got)
	}
	if got != "***(7 chars)" {
		t.Errorf("MaskValue = %q", got)
	}
}

func TestGetEnvValuesMasksByDefault(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/resources":
			w.Write([]byte(`[{"uuid":"a1","name":"api","type":"application","status":"running"}]`))
		case "/api/v1/applications/a1/envs":
			w.Write([]byte(`[{"uuid":"e1","key":"SECRET","value":"hunter2"},{"uuid":"e2","key":"EMPTY","value":""}]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	vars, err := c.GetEnvValues(context.Background(), "a1", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vars {
		if v.Value == "hunter2" {
			t.Fatal("masked read returned the real value")
		}
		if !v.Masked {
			t.Errorf("%s is not flagged as masked", v.Key)
		}
	}
	unmasked, err := c.GetEnvValues(context.Background(), "a1", false)
	if err != nil {
		t.Fatal(err)
	}
	if unmasked[1].Value != "hunter2" {
		t.Errorf("unmasked read = %+v", unmasked)
	}
}

func TestGetDatabaseCredentialsProjectsAndMasks(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/resources":
			w.Write([]byte(`[{"uuid":"d1","name":"pg","type":"standalone-postgresql","status":"running"}]`))
		case "/api/v1/databases/d1":
			w.Write([]byte(`{
				"uuid":"d1","name":"pg","status":"running",
				"postgres_user":"app","postgres_password":"s3cret","postgres_db":"app",
				"internal_db_url":"postgres://app:s3cret@pg:5432/app",
				"is_public":false,
				"custom_unrelated_field":"should not be returned",
				"private_key":"-----BEGIN OPENSSH PRIVATE KEY-----"
			}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	creds, err := c.GetDatabaseCredentials(context.Background(), "d1", true)
	if err != nil {
		t.Fatal(err)
	}
	// Only allowlisted credential fields come back.
	for _, forbidden := range []string{"custom_unrelated_field", "private_key"} {
		if _, ok := creds.Credentials[forbidden]; ok {
			t.Errorf("%s must not be returned", forbidden)
		}
	}
	if creds.Credentials["postgres_user"] != "app" {
		t.Errorf("non-secret fields stay readable, got %v", creds.Credentials["postgres_user"])
	}
	blob, _ := json.Marshal(creds)
	if strings.Contains(string(blob), "s3cret") {
		t.Fatalf("masked credentials leaked the password: %s", blob)
	}
	if creds.Engine != "postgresql" {
		t.Errorf("engine = %q", creds.Engine)
	}

	unmasked, err := c.GetDatabaseCredentials(context.Background(), "d1", false)
	if err != nil {
		t.Fatal(err)
	}
	if unmasked.Credentials["postgres_password"] != "s3cret" {
		t.Errorf("unmasked read = %v", unmasked.Credentials["postgres_password"])
	}
}

// get_database_credentials must refuse an application: its secrets are env vars.
func TestGetDatabaseCredentialsRefusesNonDatabases(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/resources":
			w.Write([]byte(`[{"uuid":"a1","name":"api","type":"application","status":"running"}]`))
		case "/api/v1/applications/a1":
			w.Write([]byte(`{"uuid":"a1","name":"api","status":"running"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	if _, err := c.GetDatabaseCredentials(context.Background(), "a1", true); !guard.IsCode(err, guard.CodeBadInput) {
		t.Fatalf("want BAD_INPUT, got %v", err)
	}
}

func assertAuditContains(t *testing.T, a *guard.Auditor, needles ...string) {
	t.Helper()
	data := readFile(t, a.Path())
	for _, n := range needles {
		if !strings.Contains(data, n) {
			t.Errorf("audit log is missing %q\n%s", n, data)
		}
	}
}
