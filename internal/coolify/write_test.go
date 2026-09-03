package coolify

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"coolify-mcp/internal/guard"
)

// mutationFixture serves one resource with a controllable status and records
// whether a mutating request ever arrived.
type mutationFixture struct {
	client   *Client
	audit    *guard.Auditor
	status   string
	kind     string
	mutated  int32
	lastBody string
}

func newMutationFixture(t *testing.T, kind, status string) *mutationFixture {
	t.Helper()
	f := &mutationFixture{status: status, kind: kind}
	segment := map[string]string{"application": "applications", "database": "databases", "service": "services"}[kind]
	rawType := map[string]string{"application": "application", "database": "standalone-postgresql", "service": "service"}[kind]

	c, audit, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/resources":
			w.Write([]byte(`[{"uuid":"r1","name":"thing","type":"` + rawType + `","status":"` + f.status + `"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/"+segment+"/r1":
			w.Write([]byte(`{"uuid":"r1","name":"thing","status":"` + f.status + `"}`))
		case r.Method == http.MethodPatch || r.Method == http.MethodPost:
			atomic.AddInt32(&f.mutated, 1)
			body, _ := io.ReadAll(r.Body)
			f.lastBody = string(body)
			w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	f.client, f.audit = c, audit
	return f
}

func (f *mutationFixture) didMutate() bool { return atomic.LoadInt32(&f.mutated) > 0 }

// R2 across the state table, for every configuration mutation.
func TestR2BlocksConfigMutationsOnAir(t *testing.T) {
	activeStates := []string{"running", "running:healthy", "running:unhealthy", "degraded", "starting", "restarting", "deploying"}
	inactiveStates := []string{"exited", "stopped", "created", "paused", "not_deployed"}

	mutations := map[string]func(*Client, *guard.OnAirGuard, bool) (*Mutation, error){
		"update_application_config": func(c *Client, g *guard.OnAirGuard, confirm bool) (*Mutation, error) {
			return c.UpdateApplicationConfig(context.Background(), g, "r1", map[string]any{"build_command": "make"}, confirm)
		},
		"upsert_env": func(c *Client, g *guard.OnAirGuard, confirm bool) (*Mutation, error) {
			return c.UpsertEnv(context.Background(), g, "r1", []EnvInput{{Key: "K", Value: "V"}}, confirm)
		},
		"update_domains": func(c *Client, g *guard.OnAirGuard, confirm bool) (*Mutation, error) {
			return c.UpdateDomains(context.Background(), g, "r1", []string{"https://a.example.com"}, confirm)
		},
	}

	for name, mutate := range mutations {
		for _, status := range activeStates {
			f := newMutationFixture(t, "application", status)
			g := guard.NewOnAirGuard(true)

			if _, err := mutate(f.client, g, false); !guard.IsCode(err, guard.CodeDeniedOnAir) {
				t.Errorf("%s on %q: want DENIED_ONAIR, got %v", name, status, err)
			}
			if f.didMutate() {
				t.Errorf("%s on %q: a refused mutation still reached Coolify", name, status)
			}

			m, err := mutate(f.client, g, true)
			if err != nil {
				t.Errorf("%s on %q with confirm: want allow, got %v", name, status, err)
				continue
			}
			if !m.HotEdited || m.Note == "" {
				t.Errorf("%s on %q with confirm: the result must flag the hot edit, got %+v", name, status, m)
			}
		}

		for _, status := range inactiveStates {
			f := newMutationFixture(t, "application", status)
			m, err := mutate(f.client, guard.NewOnAirGuard(true), false)
			if err != nil {
				t.Errorf("%s on %q: want allow, got %v", name, status, err)
				continue
			}
			if m.HotEdited {
				t.Errorf("%s on %q: a stopped resource is not a hot edit", name, status)
			}
			if !f.didMutate() {
				t.Errorf("%s on %q: the mutation never reached Coolify", name, status)
			}
		}
	}
}

func TestR2UnknownStatusFailsClosed(t *testing.T) {
	f := newMutationFixture(t, "application", "who-knows")
	if _, err := f.client.UpsertEnv(context.Background(), guard.NewOnAirGuard(true), "r1", []EnvInput{{Key: "K", Value: "V"}}, false); !guard.IsCode(err, guard.CodeDeniedOnAir) {
		t.Fatalf("strict: want DENIED_ONAIR, got %v", err)
	}
	if f.didMutate() {
		t.Fatal("strict mode still wrote")
	}
	if _, err := f.client.UpsertEnv(context.Background(), guard.NewOnAirGuard(false), "r1", []EnvInput{{Key: "K", Value: "V"}}, false); err != nil {
		t.Fatalf("non-strict: want allow, got %v", err)
	}
}

// repair_resource recreates a container, so confirm=true must not open it up.
func TestRepairRequiresAStoppedResource(t *testing.T) {
	for _, status := range []string{"running", "running:healthy", "degraded", "deploying", "who-knows"} {
		f := newMutationFixture(t, "application", status)
		if _, err := f.client.RepairResource(context.Background(), guard.NewOnAirGuard(true), "r1"); !guard.IsCode(err, guard.CodeDeniedOnAir) {
			t.Errorf("repair on %q: want DENIED_ONAIR, got %v", status, err)
		}
		if f.didMutate() {
			t.Errorf("repair on %q reached Coolify anyway", status)
		}
	}
	f := newMutationFixture(t, "application", "exited")
	m, err := f.client.RepairResource(context.Background(), guard.NewOnAirGuard(true), "r1")
	if err != nil {
		t.Fatalf("repair on a stopped resource: %v", err)
	}
	if !f.didMutate() || m.Note == "" {
		t.Fatalf("repair did not run: %+v", m)
	}
}

// Creation never deploys implicitly.
func TestCreationIsAlwaysStopped(t *testing.T) {
	f := newMutationFixture(t, "application", "exited")
	ctx := context.Background()
	fields := map[string]any{
		"project_uuid": "p", "server_uuid": "s", "environment_name": "production",
		"git_repository": "https://github.com/x/y", "git_branch": "main",
		"build_pack": "nixpacks", "instant_deploy": true,
	}
	if _, err := f.client.CreateApplication(ctx, SourcePublicRepo, fields); err != nil {
		t.Fatal(err)
	}
	assertInstantDeployFalse(t, f.lastBody)

	if _, err := f.client.CreateDatabase(ctx, "postgresql", map[string]any{"instant_deploy": true}); err != nil {
		t.Fatal(err)
	}
	assertInstantDeployFalse(t, f.lastBody)

	if _, err := f.client.CreateService(ctx, map[string]any{"type": "plausible", "instant_deploy": true}); err != nil {
		t.Fatal(err)
	}
	assertInstantDeployFalse(t, f.lastBody)
}

// A config patch must not be able to smuggle a deployment in through
// instant_deploy: deploying stays an explicit tool call.
func TestUpdateConfigStripsInstantDeploy(t *testing.T) {
	f := newMutationFixture(t, "application", "exited")
	_, err := f.client.UpdateApplicationConfig(context.Background(), guard.NewOnAirGuard(true), "r1",
		map[string]any{"build_command": "make", "instant_deploy": true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.lastBody, "instant_deploy") {
		t.Fatalf("instant_deploy survived into the patch: %s", f.lastBody)
	}
}

func TestUpdateApplicationConfigRejectsNonApplications(t *testing.T) {
	f := newMutationFixture(t, "database", "exited")
	_, err := f.client.UpdateApplicationConfig(context.Background(), guard.NewOnAirGuard(true), "r1",
		map[string]any{"build_command": "make"}, false)
	if !guard.IsCode(err, guard.CodeBadInput) {
		t.Fatalf("want BAD_INPUT, got %v", err)
	}
}

func TestUpdateDomainsRejectsDatabasesAndEmptyLists(t *testing.T) {
	f := newMutationFixture(t, "database", "exited")
	if _, err := f.client.UpdateDomains(context.Background(), guard.NewOnAirGuard(true), "r1", []string{"a.example.com"}, false); !guard.IsCode(err, guard.CodeBadInput) {
		t.Fatalf("database: want BAD_INPUT, got %v", err)
	}
	app := newMutationFixture(t, "application", "exited")
	for _, domains := range [][]string{nil, {}, {"", "  "}} {
		if _, err := app.client.UpdateDomains(context.Background(), guard.NewOnAirGuard(true), "r1", domains, false); !guard.IsCode(err, guard.CodeBadInput) {
			t.Errorf("domains=%v: want BAD_INPUT (this tool cannot clear the list), got %v", domains, err)
		}
	}
}

// Lifecycle is exempt from R2: stopping a running resource is the point.
func TestLifecycleIgnoresR2(t *testing.T) {
	f := newMutationFixture(t, "application", "running:healthy")
	for _, action := range []string{ActionStart, ActionStop, ActionRestart} {
		if _, err := f.client.Control(context.Background(), "r1", action); err != nil {
			t.Errorf("control(%s) on a running resource: want allow, got %v", action, err)
		}
	}
	if _, err := f.client.Control(context.Background(), "r1", "obliterate"); !guard.IsCode(err, guard.CodeBadInput) {
		t.Errorf("unknown action: want BAD_INPUT, got %v", err)
	}
}

func TestUpsertEnvSendsABulkPatch(t *testing.T) {
	f := newMutationFixture(t, "application", "stopped")
	_, err := f.client.UpsertEnv(context.Background(), guard.NewOnAirGuard(true), "r1", []EnvInput{
		{Key: "A", Value: "1"},
		{Key: "B", Value: "2", IsBuildTime: true},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(f.lastBody), &body); err != nil {
		t.Fatalf("body is not the bulk shape: %s", f.lastBody)
	}
	if len(body.Data) != 2 || body.Data[0]["key"] != "A" || body.Data[1]["is_build_time"] != true {
		t.Errorf("bulk payload = %s", f.lastBody)
	}
	if _, err := f.client.UpsertEnv(context.Background(), guard.NewOnAirGuard(true), "r1", []EnvInput{{Key: " ", Value: "x"}}, false); !guard.IsCode(err, guard.CodeBadInput) {
		t.Errorf("empty key: want BAD_INPUT, got %v", err)
	}
}

func assertInstantDeployFalse(t *testing.T, body string) {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("body is not JSON: %s", body)
	}
	if m["instant_deploy"] != false {
		t.Errorf("instant_deploy = %v, want false; creation must never deploy: %s", m["instant_deploy"], body)
	}
}

func TestEncodeComposeBase64(t *testing.T) {
	yaml := "services:\n  web:\n    image: nginx\n"
	got := encodeCompose(yaml)
	decoded, err := base64.StdEncoding.DecodeString(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != yaml {
		t.Fatalf("roundtrip = %q", decoded)
	}
	if encodeCompose(got) != got {
		t.Fatal("already-encoded compose was encoded twice")
	}
}

func TestUpdateApplicationConfigPatchesStoppedServiceCompose(t *testing.T) {
	f := newMutationFixture(t, "service", "exited")
	g := guard.NewOnAirGuard(true)
	_, err := f.client.UpdateApplicationConfig(context.Background(), g, "r1", map[string]any{
		"docker_compose_raw": "services:\n  glances:\n    image: nicolargo/glances:latest\n",
		"urls": []map[string]string{
			{"name": "glances", "url": ""},
			{"name": "glances-proxy", "url": "https://beholder.example.com"},
		},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !f.didMutate() {
		t.Fatal("expected a PATCH to Coolify")
	}
	if !strings.Contains(f.lastBody, `"docker_compose_raw"`) {
		t.Fatalf("body missing compose: %s", f.lastBody)
	}
	if strings.Contains(f.lastBody, "services:\n") {
		t.Fatalf("compose was sent as raw YAML, Coolify wants base64: %s", f.lastBody)
	}
}
