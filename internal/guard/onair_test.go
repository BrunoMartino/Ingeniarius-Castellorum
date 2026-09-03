package guard

import (
	"strings"
	"testing"
)

// The state table from the spec, exhaustively: every documented status against
// every mode a configuration mutation can be attempted in.
func TestClassify(t *testing.T) {
	cases := map[string]State{
		"running":            StateActive,
		"running:healthy":    StateActive,
		"running:unhealthy":  StateActive,
		"degraded":           StateActive,
		"starting":           StateActive,
		"restarting":         StateActive,
		"deploying":          StateActive,
		"RUNNING":            StateActive,
		"  running:healthy ": StateActive,
		"deploying:queued":   StateActive,

		"exited":            StateInactive,
		"stopped":           StateInactive,
		"created":           StateInactive,
		"paused":            StateInactive,
		"not_deployed":      StateInactive,
		"exited:unhealthy":  StateInactive,
		"stopped:unhealthy": StateInactive,

		"":            StateUnknown,
		"unknown":     StateUnknown,
		"weird-state": StateUnknown,
	}
	for status, want := range cases {
		if got := Classify(status); got != want {
			t.Errorf("Classify(%q) = %q, want %q", status, got, want)
		}
	}
}

func TestAssertMutableStateTable(t *testing.T) {
	active := []string{"running", "running:healthy", "running:unhealthy", "degraded", "starting", "restarting", "deploying"}
	inactive := []string{"exited", "stopped", "created", "paused", "not_deployed"}

	strict := NewOnAirGuard(true)
	lax := NewOnAirGuard(false)

	for _, status := range inactive {
		for _, g := range []*OnAirGuard{strict, lax} {
			if err := g.AssertMutable("x", status); err != nil {
				t.Errorf("status %q strict=%v: want allow, got %v", status, g.Strict, err)
			}
		}
	}

	// A running resource is refused in every mode. There is no escape hatch:
	// this is the guard a confirm flag used to be able to walk straight past.
	for _, status := range active {
		for _, g := range []*OnAirGuard{strict, lax} {
			if err := g.AssertMutable("x", status); !IsCode(err, CodeDeniedOnAir) {
				t.Errorf("status %q strict=%v: want DENIED_ONAIR, got %v", status, g.Strict, err)
			}
		}
	}
}

// The refusal must hand the decision back to the human, not point the agent at
// control(stop).
func TestOnAirRefusalTellsTheAgentToAskTheHuman(t *testing.T) {
	err := NewOnAirGuard(true).AssertMutable("abc", "running:healthy")
	if !IsCode(err, CodeDeniedOnAir) {
		t.Fatalf("want DENIED_ONAIR, got %v", err)
	}
	msg := err.Error()
	for _, want := range []string{"cannot be changed or repaired", "ask the human", "Do NOT stop it yourself"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal should say %q, got: %s", want, msg)
		}
	}
}

// Nothing anywhere may still advertise the removed escape hatch.
func TestNothingAdvertisesConfirm(t *testing.T) {
	g := NewOnAirGuard(true)
	for _, status := range []string{"running", "deploying", "unknown", ""} {
		for _, err := range []error{g.AssertMutable("x", status), g.AssertStopped("x", status)} {
			if err != nil && strings.Contains(strings.ToLower(err.Error()), "confirm") {
				t.Errorf("status %q: refusal still mentions confirm: %v", status, err)
			}
		}
	}
}

func TestAssertMutableUnknownFailsClosedUnderStrict(t *testing.T) {
	for _, status := range []string{"", "unknown", "something-new"} {
		if err := NewOnAirGuard(true).AssertMutable("x", status); !IsCode(err, CodeDeniedOnAir) {
			t.Errorf("strict, status %q: want DENIED_ONAIR, got %v", status, err)
		}
		if err := NewOnAirGuard(false).AssertMutable("x", status); err != nil {
			t.Errorf("non-strict, status %q: want allow, got %v", status, err)
		}
	}
}

// repair_resource recreates a container, so it demands a provably stopped
// resource and rejects an unknown status even outside strict mode.
func TestAssertStoppedRequiresAStoppedResource(t *testing.T) {
	g := NewOnAirGuard(true)
	for _, status := range []string{"running", "running:healthy", "degraded", "deploying", "unknown", ""} {
		if err := g.AssertStopped("x", status); !IsCode(err, CodeDeniedOnAir) {
			t.Errorf("status %q: want DENIED_ONAIR, got %v", status, err)
		}
	}
	for _, status := range []string{"exited", "stopped", "created", "paused", "not_deployed"} {
		if err := g.AssertStopped("x", status); err != nil {
			t.Errorf("status %q: want allow, got %v", status, err)
		}
	}
}

func TestOnAirErrorCarriesTheRemedy(t *testing.T) {
	err := NewOnAirGuard(true).AssertMutable("abc", "running")
	var ge Error
	if !asError(err, &ge) {
		t.Fatalf("want a guard.Error, got %T", err)
	}
	if ge.Remedy == "" {
		t.Fatal("an on-air refusal must tell the agent the way forward")
	}
	for _, want := range []string{"stop", "deploy"} {
		if !strings.Contains(strings.ToLower(ge.Remedy), want) {
			t.Errorf("remedy %q should mention %q", ge.Remedy, want)
		}
	}
}

func asError(err error, dest *Error) bool {
	ge, ok := err.(Error)
	if ok {
		*dest = ge
	}
	return ok
}
