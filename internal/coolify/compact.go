package coolify

import (
	"encoding/json"
)

// PruneJSON removes null, empty string, empty array and empty object values
// recursively. Non-empty values are preserved.
func PruneJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	pruned := pruneValue(v)
	if pruned == nil {
		return json.RawMessage(`null`)
	}
	out, err := json.Marshal(pruned)
	if err != nil {
		return raw
	}
	return out
}

func pruneValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range x {
			if pruned := pruneValue(val); !isEmpty(pruned) {
				out[k] = pruned
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case []any:
		out := make([]any, 0, len(x))
		for _, item := range x {
			if pruned := pruneValue(item); !isEmpty(pruned) {
				out = append(out, pruned)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	default:
		return v
	}
}

func isEmpty(v any) bool {
	if v == nil {
		return true
	}
	switch x := v.(type) {
	case string:
		return x == ""
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	default:
		return false
	}
}

const maxDeploymentLogBytes = 8192

// TrimDeploymentLogs strips or truncates deployment build logs to save tokens.
// List responses drop logs entirely; detail responses keep the last N bytes.
func TrimDeploymentLogs(raw json.RawMessage, detail bool) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	trimmed := trimDeploymentValue(v, detail)
	out, err := json.Marshal(trimmed)
	if err != nil {
		return raw
	}
	return out
}

func trimDeploymentValue(v any, detail bool) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range x {
			if k == "logs" {
				if s, ok := val.(string); ok {
					if !detail {
						continue
					}
					out[k] = truncateTail(s, maxDeploymentLogBytes)
					continue
				}
			}
			out[k] = trimDeploymentValue(val, detail)
		}
		return out
	case []any:
		out := make([]any, 0, len(x))
		for _, item := range x {
			out = append(out, trimDeploymentValue(item, detail))
		}
		return out
	default:
		return v
	}
}

func truncateTail(s string, max int) string {
	if len(s) <= max {
		return s
	}
	const ellipsis = "…"
	if max <= len(ellipsis) {
		return s[len(s)-max:]
	}
	return ellipsis + s[len(s)-(max-len(ellipsis)):]
}
