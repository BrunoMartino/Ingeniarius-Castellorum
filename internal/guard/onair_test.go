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
			for _, confirm := range []bool{false, true} {
				if err := g.AssertMutable("x", status, confirm); err != nil {
					t.Errorf("status %q strict=%v confirm=%v: want allow, got %v", status, g.Strict, confirm, err)
				}
			}
		}
	}

	for _, status := range active {
		for _, g := range []*OnAirGuard{strict, lax} {
			if err := g.AssertMutable("x", status, false); !IsCode(err, CodeDeniedOnAir) {
				t.Errorf("status %q strict=%v: want DENIED_ONAIR, got %v", status, g.Strict, err)
			}
			// confirm=true is the documented hot-edit escape hatch.
			if err := g.AssertMutable("x", status, true); err != nil {
				t.Errorf("status %q with confirm: want allow, got %v", status, err)
			}
		}
	}
}

func TestAssertMutableUnknownFailsClosedUnderStrict(t *testing.T) {
	for _, status := range []string{"", "unknown", "something-new"} {
		if err := NewOnAirGuard(true).AssertMutable("x", status, false); !IsCode(err, CodeDeniedOnAir) {
			t.Errorf("strict, status %q: want DENIED_ONAIR, got %v", status, err)
		}
		if err := NewOnAirGuard(false).AssertMutable("x", status, false); err != nil {
			t.Errorf("non-strict, status %q: want allow, got %v", status, err)
		}
	}
}

// repair_resource must not be reachable with confirm=true on a live resource.
func TestAssertStoppedHasNoConfirmEscape(t *testing.T) {
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
	err := NewOnAirGuard(true).AssertMutable("abc", "running", false)
	var ge Error
	if !asError(err, &ge) {
		t.Fatalf("want a guard.Error, got %T", err)
	}
	if ge.Remedy == "" {
		t.Fatal("an on-air refusal must tell the agent the way forward")
	}
	for _, want := range []string{"confirm=true", "control(stop)", "deploy"} {
		if !strings.Contains(ge.Remedy, want) {
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
