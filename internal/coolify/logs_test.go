package coolify

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"coolify-mcp/internal/guard"
)

// A service has no /logs endpoint of its own. The regression this guards is
// real: Coolify 404s it, and get_logs used to surface that 404 to the agent.
func TestServiceLogsFanOutOverContainers(t *testing.T) {
	var serviceLogsHit int
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/resources":
			w.Write([]byte(`[{"uuid":"s1","name":"stack","type":"service","status":"running:healthy"}]`))
		case "/api/v1/services/s1":
			w.Write([]byte(`{"uuid":"s1","name":"stack",
				"applications":[{"uuid":"a1","name":"proxy"},{"uuid":"a2","name":"web"}],
				"databases":[{"uuid":"d1","name":"pg"}]}`))
		case "/api/v1/services/s1/logs":
			serviceLogsHit++
			w.WriteHeader(http.StatusNotFound)
		case "/api/v1/services/s1/applications/a1/logs":
			w.Write([]byte(`{"logs":"proxy log"}`))
		case "/api/v1/services/s1/applications/a2/logs":
			w.Write([]byte(`{"logs":"web log"}`))
		case "/api/v1/services/s1/databases/d1/logs":
			w.Write([]byte(`{"logs":"pg log"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	got, err := c.Logs(context.Background(), "s1", 50, "")
	if err != nil {
		t.Fatalf("service logs must not fail: %v", err)
	}
	if serviceLogsHit != 0 {
		t.Error("the dead /services/{uuid}/logs endpoint must not be called")
	}
	if len(got) != 3 {
		t.Fatalf("got %d containers, want 3: %+v", len(got), got)
	}
	names := []string{}
	for _, g := range got {
		names = append(names, g.Name)
		if len(g.Logs) == 0 || g.Err != "" {
			t.Errorf("container %s returned no logs: %+v", g.Name, g)
		}
	}
	if strings.Join(names, ",") != "proxy,web,pg" {
		t.Errorf("containers = %v", names)
	}
}

func TestServiceLogsCanSelectOneContainer(t *testing.T) {
	c := serviceLogsClient(t)
	for _, selector := range []string{"proxy", "a1", "PROXY"} {
		got, err := c.Logs(context.Background(), "s1", 50, selector)
		if err != nil {
			t.Fatalf("selector %q: %v", selector, err)
		}
		if len(got) != 1 || got[0].Name != "proxy" {
			t.Errorf("selector %q returned %+v", selector, got)
		}
	}
	_, err := c.Logs(context.Background(), "s1", 50, "nope")
	if !guard.IsCode(err, guard.CodeNotFound) {
		t.Fatalf("unknown container: want NOT_FOUND, got %v", err)
	}
	if !strings.Contains(err.Error(), "proxy") {
		t.Errorf("the error should list the real container names: %v", err)
	}
}

// One unreadable container must not hide the others: the broken one is usually
// the one whose logs matter.
func TestServiceLogsSurviveOneBadContainer(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/resources":
			w.Write([]byte(`[{"uuid":"s1","name":"stack","type":"service","status":"running"}]`))
		case "/api/v1/services/s1":
			w.Write([]byte(`{"uuid":"s1","applications":[{"uuid":"a1","name":"proxy"},{"uuid":"a2","name":"web"}]}`))
		case "/api/v1/services/s1/applications/a1/logs":
			w.WriteHeader(http.StatusInternalServerError)
		case "/api/v1/services/s1/applications/a2/logs":
			w.Write([]byte(`{"logs":"web log"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	got, err := c.Logs(context.Background(), "s1", 50, "")
	if err != nil {
		t.Fatalf("one bad container must not fail the call: %v", err)
	}
	if len(got) != 2 || got[0].Err == "" || len(got[1].Logs) == 0 {
		t.Fatalf("got %+v", got)
	}
}

// Applications keep the single-endpoint path and reject the container filter.
func TestApplicationLogsStaySingleContainer(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/resources":
			w.Write([]byte(`[{"uuid":"a1","name":"api","type":"application","status":"running"}]`))
		case "/api/v1/applications/a1/logs":
			w.Write([]byte(`{"logs":"api log"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	got, err := c.Logs(context.Background(), "a1", 50, "")
	if err != nil || len(got) != 1 || got[0].Kind != "application" {
		t.Fatalf("got %+v, err %v", got, err)
	}
	if _, err := c.Logs(context.Background(), "a1", 50, "something"); !guard.IsCode(err, guard.CodeBadInput) {
		t.Errorf("container on an application: want BAD_INPUT, got %v", err)
	}
}

// --- the settle window ---

// A status read right after a deploy must be flagged, because Docker reports a
// container inside its healthcheck start_period as healthy regardless.
func TestStatusIsProvisionalRightAfterADeploy(t *testing.T) {
	c := serviceLogsClient(t)
	ctx := context.Background()

	before, err := c.Resolve(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if before.StatusProvisional {
		t.Fatal("a resource nobody deployed must not be flagged")
	}

	if _, err := c.Deploy(ctx, "s1", DeployOptions{}); err != nil {
		t.Fatal(err)
	}
	after, err := c.Resolve(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if !after.StatusProvisional {
		t.Fatal("a status read just after a deploy must be flagged as provisional")
	}
	for _, want := range []string{"start_period", "Do NOT report success"} {
		if !strings.Contains(after.StatusNote, want) {
			t.Errorf("the note should mention %q, got %q", want, after.StatusNote)
		}
	}
	// The flag must reach the list paths too, not just Resolve.
	items, err := c.Search(ctx, SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || !items[0].StatusProvisional {
		t.Errorf("search results must carry the flag: %+v", items)
	}
}

func TestSettleWindowExpires(t *testing.T) {
	c := serviceLogsClient(t)
	c.deploys.mark("s1")
	if _, settling := c.deploys.since("s1"); !settling {
		t.Fatal("just marked, must be settling")
	}
	c.deploys.mu.Lock()
	c.deploys.at["s1"] = time.Now().Add(-settleWindow - time.Second)
	c.deploys.mu.Unlock()
	if _, settling := c.deploys.since("s1"); settling {
		t.Fatal("past the window, must no longer be settling")
	}
	res, err := c.Resolve(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusProvisional || res.StatusNote != "" {
		t.Errorf("a settled resource must be clean: %+v", res)
	}
}

// stop does not restart containers, so it must not open a settle window.
func TestControlMarksStartAndRestartButNotStop(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		action string
		want   bool
	}{
		{ActionStart, true},
		{ActionRestart, true},
		{ActionStop, false},
	} {
		c := serviceLogsClient(t)
		if _, err := c.Control(ctx, "s1", tc.action); err != nil {
			t.Fatalf("%s: %v", tc.action, err)
		}
		if _, settling := c.deploys.since("s1"); settling != tc.want {
			t.Errorf("after %s: settling=%v, want %v", tc.action, settling, tc.want)
		}
	}
}

func serviceLogsClient(t *testing.T) *Client {
	t.Helper()
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/resources":
			w.Write([]byte(`[{"uuid":"s1","name":"stack","type":"service","status":"running:healthy"}]`))
		case r.URL.Path == "/api/v1/services/s1":
			w.Write([]byte(`{"uuid":"s1","name":"stack","status":"running:healthy",
				"applications":[{"uuid":"a1","name":"proxy"},{"uuid":"a2","name":"web"}],
				"databases":[{"uuid":"d1","name":"pg"}]}`))
		case strings.HasSuffix(r.URL.Path, "/logs"):
			w.Write([]byte(`{"logs":"log"}`))
		case r.Method == http.MethodPost:
			w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	return c
}
