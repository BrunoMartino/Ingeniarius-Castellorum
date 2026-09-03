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

const onAirRemedy = "ask the human to stop the resource in Coolify, or to authorise control(stop) explicitly. Do NOT stop it yourself. Once it is stopped: the update, then deploy(uuid)."

// AssertMutable gates a configuration mutation. A resource that is on air is
// refused, full stop — there is no confirm flag and no escape hatch.
//
// This used to allow an in-place edit with confirm=true. That is exactly how a
// failing healthcheck reached a live service: the edit was accepted while the
// resource was serving traffic, and the damage only surfaced later, when
// Traefik dropped the container from its load balancer. Editing the definition
// of something that is currently running is not a decision this server makes
// on an agent's say-so.
//
// Lifecycle operations (control, deploy, cancel_deployment) must not call this:
// restarting or deploying a running resource is the expected behaviour.
func (g *OnAirGuard) AssertMutable(uuid, status string) error {
	switch Classify(status) {
	case StateInactive:
		return nil
	case StateActive:
		return NewErrorWithRemedy(CodeDeniedOnAir,
			"resource "+uuid+" is running (status "+status+"); its configuration and files cannot be changed or repaired while it is up",
			onAirRemedy)
	default:
		if !g.Strict {
			return nil
		}
		return NewErrorWithRemedy(CodeDeniedOnAir,
			"status of resource "+uuid+" is unknown ("+quote(status)+") and COOLIFY_MCP_STRICT_ONAIR is on, so the mutation fails closed",
			onAirRemedy)
	}
}

// AssertStopped is R2 at its strictest: the resource must be
// provably inactive. Used by repair_resource, which recreates the container.
func (g *OnAirGuard) AssertStopped(uuid, status string) error {
	if Classify(status) == StateInactive {
		return nil
	}
	return NewErrorWithRemedy(CodeDeniedOnAir,
		"resource "+uuid+" is not stopped (status "+quote(status)+"); it cannot be repaired while it is up, because this recreates its container",
		onAirRemedy)
}

func quote(s string) string {
	if s == "" {
		return `""`
	}
	return s
}
