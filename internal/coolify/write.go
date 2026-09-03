package coolify

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"unicode"

	"coolify-mcp/internal/guard"
)

// CreateProject creates an empty project. Projects have no runtime state, so
// R2 does not apply.
func (c *Client) CreateProject(ctx context.Context, name, description string) (json.RawMessage, error) {
	if strings.TrimSpace(name) == "" {
		return nil, guard.NewError(guard.CodeBadInput, "name is required")
	}
	body := map[string]any{"name": name}
	if description != "" {
		body["description"] = description
	}
	var out json.RawMessage
	if err := c.Post(ctx, "/projects", nil, body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Application sources map onto Coolify's four creation endpoints.
const (
	SourcePublicRepo       = "public_repo"
	SourcePrivateRepoGH    = "private_repo_github"
	SourceDockerfile       = "dockerfile"
	SourceDockerCompose    = "docker_compose"
	SourceDockerImage      = "docker_image"
	buildPackDockerCompose = "dockercompose"
)

// CreateApplication provisions an application. The resource is created stopped:
// deploying is a separate, explicit step, so the agent never brings something
// live as a side effect of creating it.
func (c *Client) CreateApplication(ctx context.Context, source string, fields map[string]any) (json.RawMessage, error) {
	if fields == nil {
		fields = map[string]any{}
	}
	var path string
	switch strings.ToLower(strings.TrimSpace(source)) {
	case SourcePublicRepo:
		path = "/applications/public"
	case SourcePrivateRepoGH:
		path = "/applications/private-github-app"
	case SourceDockerfile:
		path = "/applications/dockerfile"
	case SourceDockerCompose:
		// Coolify has no compose-specific endpoint: a compose app is a git
		// application whose build pack is dockercompose.
		path = "/applications/public"
		fields["build_pack"] = buildPackDockerCompose
	case SourceDockerImage:
		path = "/applications/dockerimage"
	default:
		return nil, guard.NewError(guard.CodeBadInput,
			"source must be one of public_repo, private_repo_github, dockerfile, docker_compose, docker_image")
	}
	// Never let a creation call deploy implicitly.
	fields["instant_deploy"] = false

	var out json.RawMessage
	if err := c.Post(ctx, path, nil, fields, &out); err != nil {
		return nil, err
	}
	c.cache.invalidate()
	return out, nil
}

var databaseEngines = map[string]bool{
	"postgresql": true, "mysql": true, "mariadb": true, "mongodb": true,
	"redis": true, "keydb": true, "clickhouse": true, "dragonfly": true,
}

// CreateDatabase provisions a managed database, stopped.
func (c *Client) CreateDatabase(ctx context.Context, engine string, fields map[string]any) (json.RawMessage, error) {
	engine = strings.ToLower(strings.TrimSpace(engine))
	if engine == "postgres" {
		engine = "postgresql"
	}
	if !databaseEngines[engine] {
		return nil, guard.NewError(guard.CodeBadInput,
			"engine must be one of postgresql, mysql, mariadb, mongodb, redis, keydb, clickhouse, dragonfly")
	}
	if fields == nil {
		fields = map[string]any{}
	}
	fields["instant_deploy"] = false

	var out json.RawMessage
	if err := c.Post(ctx, "/databases/"+engine, nil, fields, &out); err != nil {
		return nil, err
	}
	c.cache.invalidate()
	return out, nil
}

// CreateService provisions a one-click service, stopped.
func (c *Client) CreateService(ctx context.Context, fields map[string]any) (json.RawMessage, error) {
	if fields == nil {
		fields = map[string]any{}
	}
	if fields["type"] == nil && fields["docker_compose_raw"] == nil {
		return nil, guard.NewError(guard.CodeBadInput,
			"either type (a one-click service type) or docker_compose_raw is required")
	}
	fields["instant_deploy"] = false

	var out json.RawMessage
	if err := c.Post(ctx, "/services", nil, fields, &out); err != nil {
		return nil, err
	}
	c.cache.invalidate()
	return out, nil
}

// Mutation is the outcome of a guarded configuration change.
type Mutation struct {
	UUID   string          `json:"uuid"`
	Kind   Kind            `json:"kind"`
	Status string          `json:"status"`
	Note   string          `json:"note,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
}

// mutateConfig is the single R2 checkpoint. Every configuration change routes
// through here: resolve the live status, ask the on-air guard, then write.
func (c *Client) mutateConfig(ctx context.Context, onAir *guard.OnAirGuard, uuid string, apply func(Resource) (json.RawMessage, error)) (*Mutation, error) {
	res, err := c.Resolve(ctx, uuid)
	if err != nil {
		return nil, err
	}
	// Re-read the live status: the inventory row may be up to one TTL stale,
	// and R2 must never decide on a stale "stopped".
	if raw, err := c.GetRaw(ctx, res.Kind.segment()+"/"+uuid, nil); err == nil {
		if fresh := resourceFromDetail(uuid, res.Kind, raw); fresh.Status != "" {
			res.Status = fresh.Status
			res.State = fresh.State
		}
	}
	if err := onAir.AssertMutable(uuid, res.Status); err != nil {
		return nil, err
	}
	out, err := apply(res)
	if err != nil {
		return nil, err
	}
	c.cache.invalidate()
	return &Mutation{UUID: uuid, Kind: res.Kind, Status: res.Status, Result: out}, nil
}

// UpdateApplicationConfig patches an application, or a service's compose/urls.
// Refused outright while the resource is running (R2).
func (c *Client) UpdateApplicationConfig(ctx context.Context, onAir *guard.OnAirGuard, uuid string, fields map[string]any) (*Mutation, error) {
	if len(fields) == 0 {
		return nil, guard.NewError(guard.CodeBadInput, "at least one settings field is required")
	}
	// instant_deploy would turn a config edit into a deployment behind the
	// agent's back; deploying stays an explicit tool call.
	delete(fields, "instant_deploy")

	return c.mutateConfig(ctx, onAir, uuid, func(res Resource) (json.RawMessage, error) {
		var out json.RawMessage
		switch res.Kind {
		case KindApplication:
			err := c.Patch(ctx, "/applications/"+uuid, fields, &out)
			return out, err
		case KindService:
			body, err := servicePatchBody(fields)
			if err != nil {
				return nil, err
			}
			err = c.Patch(ctx, "/services/"+uuid, body, &out)
			return out, err
		default:
			return nil, guard.NewErrorWithRemedy(guard.CodeBadInput,
				"update_application_config does not apply to databases; "+uuid+" is a "+string(res.Kind),
				"use upsert_env for database settings, or change them in the Coolify UI")
		}
	})
}

func servicePatchBody(fields map[string]any) (map[string]any, error) {
	allowed := map[string]bool{
		"docker_compose_raw":                true,
		"urls":                              true,
		"name":                              true,
		"description":                       true,
		"connect_to_docker_network":         true,
		"force_domain_override":             true,
		"is_container_label_escape_enabled": true,
	}
	body := map[string]any{}
	for k, v := range fields {
		if !allowed[k] {
			return nil, guard.NewError(guard.CodeBadInput,
				"service patch does not accept "+k+"; allowed: docker_compose_raw, urls, name, description")
		}
		body[k] = v
	}
	if raw, ok := body["docker_compose_raw"].(string); ok && strings.TrimSpace(raw) != "" {
		body["docker_compose_raw"] = encodeCompose(raw)
	}
	if len(body) == 0 {
		return nil, guard.NewError(guard.CodeBadInput, "at least one service settings field is required")
	}
	return body, nil
}

func encodeCompose(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return s
	}
	if decoded, err := base64.StdEncoding.DecodeString(s); err == nil {
		trimmed := strings.TrimSpace(string(decoded))
		if strings.HasPrefix(trimmed, "services:") || strings.Contains(trimmed, "\nservices:") {
			return s
		}
	}
	// Reject leftovers that look like base64 but aren't compose — Coolify
	// requires the field to be base64 of the YAML.
	if isProbablyBase64(s) && !strings.Contains(s, "services:") {
		return s
	}
	return base64.StdEncoding.EncodeToString([]byte(raw))
}

func isProbablyBase64(s string) bool {
	if len(s) < 8 || len(s)%4 != 0 {
		return false
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '+' || r == '/' || r == '=' {
			continue
		}
		return false
	}
	_, err := base64.StdEncoding.DecodeString(s)
	return err == nil
}

// EnvInput is one variable to create or update.
type EnvInput struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	IsBuildTime bool   `json:"is_build_time,omitempty"`
	IsLiteral   bool   `json:"is_literal,omitempty"`
	IsMultiline bool   `json:"is_multiline,omitempty"`
	IsPreview   bool   `json:"is_preview,omitempty"`
}

// UpsertEnv creates or updates environment variables in one batch. It never
// removes a variable: deletion is the UI's job (R1). Refused outright while the
// resource is running (R2).
func (c *Client) UpsertEnv(ctx context.Context, onAir *guard.OnAirGuard, uuid string, vars []EnvInput) (*Mutation, error) {
	if len(vars) == 0 {
		return nil, guard.NewError(guard.CodeBadInput, "at least one variable is required")
	}
	data := make([]map[string]any, 0, len(vars))
	for _, v := range vars {
		if strings.TrimSpace(v.Key) == "" {
			return nil, guard.NewError(guard.CodeBadInput, "every variable needs a non-empty key")
		}
		entry := map[string]any{"key": v.Key, "value": v.Value}
		if v.IsBuildTime {
			entry["is_build_time"] = true
		}
		if v.IsLiteral {
			entry["is_literal"] = true
		}
		if v.IsMultiline {
			entry["is_multiline"] = true
		}
		if v.IsPreview {
			entry["is_preview"] = true
		}
		data = append(data, entry)
	}
	return c.mutateConfig(ctx, onAir, uuid, func(res Resource) (json.RawMessage, error) {
		var out json.RawMessage
		err := c.Patch(ctx, res.Kind.segment()+"/"+uuid+"/envs/bulk", map[string]any{"data": data}, &out)
		return out, err
	})
}

// UpdateDomains sets the FQDNs of a resource. Applications take a comma
// separated `domains` field; services take `urls`. Refused outright while the
// resource is running (R2).
func (c *Client) UpdateDomains(ctx context.Context, onAir *guard.OnAirGuard, uuid string, domains []string) (*Mutation, error) {
	cleaned := make([]string, 0, len(domains))
	for _, d := range domains {
		if d = strings.TrimSpace(d); d != "" {
			cleaned = append(cleaned, d)
		}
	}
	if len(cleaned) == 0 {
		return nil, guard.NewError(guard.CodeBadInput,
			"at least one domain is required; this tool sets the full domain list and cannot be used to clear it")
	}
	return c.mutateConfig(ctx, onAir, uuid, func(res Resource) (json.RawMessage, error) {
		var out json.RawMessage
		switch res.Kind {
		case KindApplication:
			err := c.Patch(ctx, "/applications/"+uuid, map[string]any{"domains": strings.Join(cleaned, ",")}, &out)
			return out, err
		case KindService:
			err := c.Patch(ctx, "/services/"+uuid, map[string]any{"urls": cleaned}, &out)
			return out, err
		default:
			return nil, guard.NewError(guard.CodeBadInput,
				"managed databases are not reachable by domain; expose them with public_port in the Coolify UI instead")
		}
	})
}

// RepairResource recreates a resource's container from its current image or
// commit, without touching volumes or files. It recreates a container, so it
// requires the resource to be provably stopped, and rejects an unknown status
// even outside strict mode.
func (c *Client) RepairResource(ctx context.Context, onAir *guard.OnAirGuard, uuid string) (*Mutation, error) {
	res, err := c.Resolve(ctx, uuid)
	if err != nil {
		return nil, err
	}
	if raw, err := c.GetRaw(ctx, res.Kind.segment()+"/"+uuid, nil); err == nil {
		if fresh := resourceFromDetail(uuid, res.Kind, raw); fresh.Status != "" {
			res.Status = fresh.Status
			res.State = fresh.State
		}
	}
	if err := onAir.AssertStopped(uuid, res.Status); err != nil {
		return nil, err
	}
	// force=true makes Coolify rebuild the container definition from the
	// current configuration rather than reusing the previous one.
	q := map[string][]string{}
	if res.Kind == KindApplication {
		q["force"] = []string{"true"}
	}
	var out json.RawMessage
	if err := c.Post(ctx, res.Kind.segment()+"/"+uuid+"/start", q, nil, &out); err != nil {
		return nil, err
	}
	c.cache.invalidate()
	c.deploys.mark(uuid)
	return &Mutation{
		UUID:   uuid,
		Kind:   res.Kind,
		Status: res.Status,
		Note:   "container recreated from the current configuration; volumes and files were not touched",
		Result: out,
	}, nil
}

func notADatabase(uuid string, kind Kind) error {
	return guard.NewErrorWithRemedy(guard.CodeBadInput,
		uuid+" is a "+string(kind)+", not a managed database",
		"use get_env_values for application and service secrets")
}
