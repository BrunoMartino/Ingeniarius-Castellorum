package coolify

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPruneJSONRemovesEmptyValues(t *testing.T) {
	raw := json.RawMessage(`{"a":null,"b":"","c":[],"d":{},"e":"ok","f":{"x":null,"y":1}}`)
	got := PruneJSON(raw)
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["a"]; ok {
		t.Error("null key should be pruned")
	}
	if _, ok := m["b"]; ok {
		t.Error("empty string should be pruned")
	}
	if _, ok := m["c"]; ok {
		t.Error("empty array should be pruned")
	}
	if _, ok := m["d"]; ok {
		t.Error("empty object should be pruned")
	}
	if m["e"] != "ok" {
		t.Errorf("e = %v", m["e"])
	}
	f := m["f"].(map[string]any)
	if len(f) != 1 || f["y"].(float64) != 1 {
		t.Errorf("nested prune failed: %v", f)
	}
}

func TestTrimDeploymentLogsListDropsLogs(t *testing.T) {
	raw := json.RawMessage(`[{"uuid":"d1","logs":"very long build output"},{"uuid":"d2","status":"finished"}]`)
	got := TrimDeploymentLogs(raw, false)
	if strings.Contains(string(got), "very long") {
		t.Fatalf("list should drop logs: %s", got)
	}
	if !strings.Contains(string(got), `"uuid":"d1"`) {
		t.Fatalf("other fields should remain: %s", got)
	}
}

func TestTrimDeploymentLogsDetailTruncates(t *testing.T) {
	long := strings.Repeat("x", maxDeploymentLogBytes+100)
	raw := json.RawMessage(`{"uuid":"d1","logs":"` + long + `"}`)
	got := TrimDeploymentLogs(raw, true)
	if !strings.Contains(string(got), `"logs"`) {
		t.Fatal("detail should keep logs field")
	}
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatal(err)
	}
	logs := m["logs"].(string)
	if len(logs) > maxDeploymentLogBytes+1 {
		t.Fatalf("logs too long: %d", len(logs))
	}
	if !strings.HasPrefix(logs, "…") {
		t.Fatal("truncated logs should have ellipsis prefix")
	}
}
