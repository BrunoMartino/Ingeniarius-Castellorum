package coolify

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"coolify-mcp/internal/guard"
)

// Kind is the family a uuid belongs to. It decides which API segment to use.
type Kind string

const (
	KindApplication Kind = "application"
	KindDatabase    Kind = "database"
	KindService     Kind = "service"
)

// segment is the URL segment for the kind: /applications, /databases, /services.
func (k Kind) segment() string {
	switch k {
	case KindApplication:
		return "/applications"
	case KindDatabase:
		return "/databases"
	case KindService:
		return "/services"
	}
	return ""
}

// Resource is the unified inventory row for an app, database or service.
type Resource struct {
	UUID        string `json:"uuid"`
	Name        string `json:"name"`
	Kind        Kind   `json:"kind"`
	RawType     string `json:"type,omitempty"`
	Status      string `json:"status,omitempty"`
	State       string `json:"state,omitempty"`
	FQDN        string `json:"fqdn,omitempty"`
	Description string `json:"description,omitempty"`
	ProjectUUID string `json:"project_uuid,omitempty"`
	Environment string `json:"environment,omitempty"`
	ServerUUID  string `json:"server_uuid,omitempty"`
}

// rawResource is the loose shape of a /resources row. Coolify's OpenAPI does
// not type this endpoint, so every field is optional and parsed defensively.
type rawResource struct {
	UUID        string          `json:"uuid"`
	Name        string          `json:"name"`
	Type        string          `json:"type"`
	Status      string          `json:"status"`
	FQDN        string          `json:"fqdn"`
	Description string          `json:"description"`
	Environment json.RawMessage `json:"environment"`
	Project     json.RawMessage `json:"project"`
	ServerUUID  string          `json:"server_uuid"`
}

type inventoryCache struct {
	mu        sync.Mutex
	items     []Resource
	fetchedAt time.Time
	ttl       time.Duration
}

func newInventoryCache() *inventoryCache {
	// A6: memory only, short TTL. Long enough to spare an overview call from
	// re-fetching per resource, short enough that a deploy shows up quickly.
	return &inventoryCache{ttl: 15 * time.Second}
}

// Inventory returns every app, database and service known to the instance.
func (c *Client) Inventory(ctx context.Context, refresh bool) ([]Resource, error) {
	c.cache.mu.Lock()
	if !refresh && c.cache.items != nil && time.Since(c.cache.fetchedAt) < c.cache.ttl {
		items := c.cache.items
		c.cache.mu.Unlock()
		return items, nil
	}
	c.cache.mu.Unlock()

	var rows []rawResource
	if err := c.Get(ctx, "/resources", nil, &rows); err != nil {
		return nil, err
	}

	items := make([]Resource, 0, len(rows))
	for _, row := range rows {
		if row.UUID == "" {
			continue
		}
		kind := kindFromType(row.Type)
		if kind == "" {
			continue
		}
		items = append(items, Resource{
			UUID:        row.UUID,
			Name:        row.Name,
			Kind:        kind,
			RawType:     row.Type,
			Status:      row.Status,
			State:       string(guard.Classify(row.Status)),
			FQDN:        row.FQDN,
			Description: row.Description,
			ProjectUUID: nestedString(row.Project, "uuid"),
			Environment: nestedString(row.Environment, "name"),
			ServerUUID:  row.ServerUUID,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })

	c.cache.mu.Lock()
	c.cache.items = items
	c.cache.fetchedAt = time.Now()
	c.cache.mu.Unlock()
	return items, nil
}

// kindFromType maps Coolify's type strings onto a Kind. Applications and
// services are named directly; every other provisioned type is a managed
// database ("standalone-postgresql", "postgresql", …).
func kindFromType(raw string) Kind {
	t := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case t == "":
		return ""
	case strings.Contains(t, "application"):
		return KindApplication
	case strings.Contains(t, "service"):
		return KindService
	default:
		for _, engine := range []string{"postgres", "mysql", "mariadb", "mongo", "redis", "keydb", "clickhouse", "dragonfly"} {
			if strings.Contains(t, engine) {
				return KindDatabase
			}
		}
		return ""
	}
}

func nestedString(raw json.RawMessage, key string) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// Resolve finds the kind and current status of a uuid. It reads the cached
// inventory first, then probes the typed endpoints — a resource created since
// the last refresh is still resolvable.
func (c *Client) Resolve(ctx context.Context, uuid string) (Resource, error) {
	uuid = strings.TrimSpace(uuid)
	if uuid == "" {
		return Resource{}, guard.NewError(guard.CodeBadInput, "uuid is required")
	}
	items, err := c.Inventory(ctx, false)
	if err == nil {
		for _, item := range items {
			if item.UUID == uuid {
				return item, nil
			}
		}
	}
	for _, kind := range []Kind{KindApplication, KindDatabase, KindService} {
		raw, err := c.GetRaw(ctx, kind.segment()+"/"+uuid, nil)
		if IsNotFound(err) {
			continue
		}
		if err != nil {
			return Resource{}, err
		}
		return resourceFromDetail(uuid, kind, raw), nil
	}
	return Resource{}, guard.NewErrorWithRemedy(guard.CodeNotFound,
		"no application, database or service found with uuid "+uuid,
		"use search_resources to find the right uuid")
}

func resourceFromDetail(uuid string, kind Kind, raw json.RawMessage) Resource {
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	str := func(key string) string {
		if v, ok := m[key].(string); ok {
			return v
		}
		return ""
	}
	status := str("status")
	return Resource{
		UUID:        uuid,
		Name:        str("name"),
		Kind:        kind,
		RawType:     string(kind),
		Status:      status,
		State:       string(guard.Classify(status)),
		FQDN:        str("fqdn"),
		Description: str("description"),
	}
}

// Detail returns the full upstream record for a uuid, plus its resolved kind.
func (c *Client) Detail(ctx context.Context, uuid string) (Resource, json.RawMessage, error) {
	res, err := c.Resolve(ctx, uuid)
	if err != nil {
		return Resource{}, nil, err
	}
	raw, err := c.GetRaw(ctx, res.Kind.segment()+"/"+uuid, nil)
	if err != nil {
		return res, nil, err
	}
	// The inventory row can be up to one TTL stale; the detail call is not.
	if fresh := resourceFromDetail(uuid, res.Kind, raw); fresh.Status != "" {
		res.Status = fresh.Status
		res.State = fresh.State
	}
	return res, raw, nil
}

// SearchOptions filters the inventory.
type SearchOptions struct {
	Query       string
	Kind        string
	Project     string
	Environment string
	Status      string
}

func (c *Client) Search(ctx context.Context, opts SearchOptions) ([]Resource, error) {
	items, err := c.Inventory(ctx, false)
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(strings.TrimSpace(opts.Query))
	out := make([]Resource, 0, len(items))
	for _, item := range items {
		if q != "" && !strings.Contains(strings.ToLower(item.Name), q) &&
			!strings.Contains(strings.ToLower(item.UUID), q) &&
			!strings.Contains(strings.ToLower(item.Description), q) &&
			!strings.Contains(strings.ToLower(item.FQDN), q) {
			continue
		}
		if opts.Kind != "" && !strings.EqualFold(string(item.Kind), opts.Kind) {
			continue
		}
		if opts.Project != "" && !strings.EqualFold(item.ProjectUUID, opts.Project) {
			continue
		}
		if opts.Environment != "" && !strings.EqualFold(item.Environment, opts.Environment) {
			continue
		}
		if opts.Status != "" && !strings.EqualFold(item.State, opts.Status) && !strings.EqualFold(item.Status, opts.Status) {
			continue
		}
		out = append(out, item)
	}
	return out, nil
}

// Unhealthy returns resources that are degraded, unhealthy, or in a status this
// MCP does not recognise. Resources that are cleanly stopped are not failures
// and are excluded.
func (c *Client) Unhealthy(ctx context.Context) ([]Resource, error) {
	items, err := c.Inventory(ctx, true)
	if err != nil {
		return nil, err
	}
	out := make([]Resource, 0)
	for _, item := range items {
		status := strings.ToLower(item.Status)
		switch {
		case strings.Contains(status, "unhealthy"), strings.Contains(status, "degraded"):
			out = append(out, item)
		case guard.Classify(item.Status) == guard.StateUnknown:
			out = append(out, item)
		}
	}
	return out, nil
}

// Server is the trimmed server row used by the overview and list_servers.
type Server struct {
	UUID        string `json:"uuid"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	IP          string `json:"ip,omitempty"`
	Port        int    `json:"port,omitempty"`
	User        string `json:"user,omitempty"`
	ProxyType   string `json:"proxy_type,omitempty"`
}

func (c *Client) ListServers(ctx context.Context) ([]Server, error) {
	var servers []Server
	if err := c.Get(ctx, "/servers", nil, &servers); err != nil {
		return nil, err
	}
	return servers, nil
}

// ServerDetail is one server plus what runs on it and which domains it serves.
type ServerDetail struct {
	Server    Server          `json:"server"`
	Resources json.RawMessage `json:"resources,omitempty"`
	Domains   json.RawMessage `json:"domains,omitempty"`
}

func (c *Client) GetServer(ctx context.Context, uuid string) (*ServerDetail, error) {
	var server Server
	if err := c.Get(ctx, "/servers/"+uuid, nil, &server); err != nil {
		return nil, err
	}
	out := &ServerDetail{Server: server}
	// Resources and domains are extra colour; a failure there must not sink
	// the whole call.
	if raw, err := c.GetRaw(ctx, "/servers/"+uuid+"/resources", nil); err == nil {
		out.Resources = raw
	}
	if raw, err := c.GetRaw(ctx, "/servers/"+uuid+"/domains", nil); err == nil {
		out.Domains = raw
	}
	return out, nil
}

// Project is a project row; Environments is filled in by GetProject.
type Project struct {
	UUID         string          `json:"uuid"`
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	Environments json.RawMessage `json:"environments,omitempty"`
}

func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	var projects []Project
	if err := c.Get(ctx, "/projects", nil, &projects); err != nil {
		return nil, err
	}
	return projects, nil
}

func (c *Client) GetProject(ctx context.Context, uuid string) (*Project, error) {
	var project Project
	if err := c.Get(ctx, "/projects/"+uuid, nil, &project); err != nil {
		return nil, err
	}
	if len(project.Environments) == 0 {
		if raw, err := c.GetRaw(ctx, "/projects/"+uuid+"/environments", nil); err == nil {
			project.Environments = raw
		}
	}
	return &project, nil
}

func (c *Client) GetEnvironment(ctx context.Context, projectUUID, environment string) (json.RawMessage, error) {
	return c.GetRaw(ctx, "/projects/"+projectUUID+"/"+url.PathEscape(environment), nil)
}

// Logs fetches the log tail for a resource, resolving its kind first.
func (c *Client) Logs(ctx context.Context, uuid string, lines int) (json.RawMessage, error) {
	res, err := c.Resolve(ctx, uuid)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("lines", strconv.Itoa(lines))
	return c.GetRaw(ctx, res.Kind.segment()+"/"+uuid+"/logs", q)
}

// Storages lists persistent and file storages of a resource.
func (c *Client) Storages(ctx context.Context, uuid string) (json.RawMessage, error) {
	res, err := c.Resolve(ctx, uuid)
	if err != nil {
		return nil, err
	}
	return c.GetRaw(ctx, res.Kind.segment()+"/"+uuid+"/storages", nil)
}

// ScheduledTasks lists a resource's scheduled tasks, or one task's executions
// when taskUUID is given. Managed databases have no scheduled tasks in the API.
func (c *Client) ScheduledTasks(ctx context.Context, uuid, taskUUID string) (json.RawMessage, error) {
	res, err := c.Resolve(ctx, uuid)
	if err != nil {
		return nil, err
	}
	if res.Kind == KindDatabase {
		return nil, guard.NewError(guard.CodeBadInput,
			"managed databases do not expose scheduled tasks; only applications and services do")
	}
	base := res.Kind.segment() + "/" + uuid + "/scheduled-tasks"
	if strings.TrimSpace(taskUUID) == "" {
		return c.GetRaw(ctx, base, nil)
	}
	return c.GetRaw(ctx, base+"/"+taskUUID+"/executions", nil)
}
