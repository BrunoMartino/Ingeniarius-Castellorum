package guard

import "strings"

// State is the coarse classification of a Coolify resource status for R2.
type State string

const (
	StateActive   State = "active"
	StateInactive State = "inactive"
	StateUnknown  State = "unknown"
)

// activeStates block configuration mutation: the resource is serving traffic or
// in the middle of a transition.
var activeStates = map[string]struct{}{
	"running":           {},
	"running:healthy":   {},
	"running:unhealthy": {},
	"degraded":          {},
	"starting":          {},
	"restarting":        {},
	"deploying":         {},
}

// inactiveStates allow configuration mutation: nothing is serving.
var inactiveStates = map[string]struct{}{
	"exited":       {},
	"stopped":      {},
	"created":      {},
	"paused":       {},
	"not_deployed": {},
	"not-deployed": {},
	"dead":         {},
}

// Classify maps a raw Coolify status onto a State. Coolify reports compound
// statuses ("running:healthy", "exited:unhealthy"); the full string is matched
// first, then the part before the colon.
func Classify(status string) State {
	s := strings.ToLower(strings.TrimSpace(status))
	if s == "" {
		return StateUnknown
	}
	if _, ok := activeStates[s]; ok {
		return StateActive
	}
	if _, ok := inactiveStates[s]; ok {
		return StateInactive
	}
	head, _, found := strings.Cut(s, ":")
	if found {
		if _, ok := activeStates[head]; ok {
			return StateActive
		}
		if _, ok := inactiveStates[head]; ok {
			return StateInactive
		}
	}
	return StateUnknown
}

// OnAirGuard implements R2: configuration mutations are refused while the
// target resource is on air.
type OnAirGuard struct {
	// Strict makes an unrecognised status block the mutation (fail-closed).
	Strict bool
}

func NewOnAirGuard(strict bool) *OnAirGuard {
	return &OnAirGuard{Strict: strict}
}

const onAirRemedy = "either pass confirm=true to edit in place (the resource keeps running and the change applies on the next deploy), or run control(stop) -> the update -> deploy(uuid)"

// AssertMutable gates a configuration mutation. An active resource is refused
// unless the caller passes confirm=true, which is the deliberate hot-edit
// escape hatch. Lifecycle operations (control, deploy, cancel_deployment) must
// not call this — restarting a running resource is the expected behaviour.
func (g *OnAirGuard) AssertMutable(uuid, status string, confirmed bool) error {
	switch Classify(status) {
	case StateInactive:
		return nil
	case StateActive:
		if confirmed {
			return nil
		}
		return NewErrorWithRemedy(CodeDeniedOnAir,
			"resource "+uuid+" is on air (status "+status+"); configuration mutations are blocked by default",
			onAirRemedy)
	default:
		if !g.Strict || confirmed {
			return nil
		}
		return NewErrorWithRemedy(CodeDeniedOnAir,
			"status of resource "+uuid+" is unknown ("+quote(status)+") and COOLIFY_MCP_STRICT_ONAIR is on, so the mutation fails closed",
			onAirRemedy)
	}
}

// AssertStopped is R2 without the confirm escape hatch: the resource must be
// provably inactive. Used by repair_resource, which recreates the container.
func (g *OnAirGuard) AssertStopped(uuid, status string) error {
	if Classify(status) == StateInactive {
		return nil
	}
	return NewErrorWithRemedy(CodeDeniedOnAir,
		"resource "+uuid+" is not stopped (status "+quote(status)+") and this operation recreates its container; confirm=true does not override it",
		"run control(stop) on "+uuid+" first, then repair_resource, then deploy(uuid)")
}

func quote(s string) string {
	if s == "" {
		return `""`
	}
	return s
}
