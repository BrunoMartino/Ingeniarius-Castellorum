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

	// StatusProvisional marks a status read too soon after a deploy or start
	// to be trusted. See deployTracker.
	StatusProvisional bool   `json:"status_provisional,omitempty"`
	StatusNote        string `json:"status_note,omitempty"`
}

// settleWindow is how long after a deploy or start a status stays provisional.
// A container healthcheck reports "starting" for its whole start_period, and
// Docker only flips it to unhealthy after start_period plus retries×interval —
// so a resource read inside this window can report healthy and still be broken.
const settleWindow = 3 * time.Minute

// deployTracker remembers when this process last deployed or started each
// resource, so status reads inside the settle window can be flagged. In memory
// only (A6); a restart of this server simply forgets, which fails open on
// annotation but never on the underlying data.
type deployTracker struct {
	mu sync.Mutex
	at map[string]time.Time
}

func newDeployTracker() *deployTracker {
	return &deployTracker{at: map[string]time.Time{}}
}

func (t *deployTracker) mark(uuid string) {
	if t == nil || uuid == "" {
		return
	}
	t.mu.Lock()
	t.at[uuid] = time.Now()
	t.mu.Unlock()
}

// since reports how long ago the resource was deployed, and whether that was
// recent enough for its status to still be settling.
func (t *deployTracker) since(uuid string) (time.Duration, bool) {
	if t == nil {
		return 0, false
	}
	t.mu.Lock()
	at, ok := t.at[uuid]
	t.mu.Unlock()
	if !ok {
		return 0, false
	}
	elapsed := time.Since(at)
	return elapsed, elapsed < settleWindow
}

// annotate flags a status this process cannot yet vouch for. The note is
// deliberately blunt: reading "running:healthy" seconds after a deploy and
// calling the job done is the exact mistake it exists to prevent.
func (c *Client) annotate(res Resource) Resource {
	elapsed, settling := c.deploys.since(res.UUID)
	if !settling {
		return res
	}
	res.StatusProvisional = true
	res.StatusNote = "deployed or started " + elapsed.Truncate(time.Second).String() +
		" ago; container healthchecks may still be inside their start_period, during which Docker reports them as healthy regardless. Do NOT report success from this read: wait until " +
		settleWindow.String() + " has passed since the deploy and confirm the status again."
	return res
}

func (c *Client) annotateAll(items []Resource) []Resource {
	out := make([]Resource, len(items))
	for i, item := range items {
		out[i] = c.annotate(item)
	}
	return out
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
		return c.annotateAll(items), nil
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
	return c.annotateAll(items), nil
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
		return c.annotate(resourceFromDetail(uuid, kind, raw)), nil
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
	return c.annotate(res), raw, nil
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

// ContainerLogs is one container's log tail. A service is several containers,
// so its logs come back as a list of these rather than a single blob.
type ContainerLogs struct {
	Name string          `json:"name"`
	UUID string          `json:"uuid"`
	Kind string          `json:"kind"`
	Logs json.RawMessage `json:"logs,omitempty"`
	Err  string          `json:"error,omitempty"`
}

// serviceMembers is the sub-application and sub-database list of a service.
type serviceMembers struct {
	Applications []struct {
		UUID string `json:"uuid"`
		Name string `json:"name"`
	} `json:"applications"`
	Databases []struct {
		UUID string `json:"uuid"`
		Name string `json:"name"`
	} `json:"databases"`
}

// Logs fetches the log tail for a resource, resolving its kind first.
//
// Applications and managed databases have a single log endpoint. A service does
// not: Coolify returns 404 for /services/{uuid}/logs and serves logs per
// container instead, so this fans out over the service's members. Pass
// container to narrow it to one, by name or by uuid.
func (c *Client) Logs(ctx context.Context, uuid string, lines int, container string) ([]ContainerLogs, error) {
	res, err := c.Resolve(ctx, uuid)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("lines", strconv.Itoa(lines))

	if res.Kind != KindService {
		if container != "" {
			return nil, guard.NewError(guard.CodeBadInput,
				"container only applies to services; "+uuid+" is a "+string(res.Kind))
		}
		raw, err := c.GetRaw(ctx, res.Kind.segment()+"/"+uuid+"/logs", q)
		if err != nil {
			return nil, err
		}
		return []ContainerLogs{{Name: res.Name, UUID: uuid, Kind: string(res.Kind), Logs: raw}}, nil
	}

	var members serviceMembers
	if err := c.Get(ctx, "/services/"+uuid, nil, &members); err != nil {
		return nil, err
	}

	type target struct{ uuid, name, segment, kind string }
	targets := make([]target, 0, len(members.Applications)+len(members.Databases))
	for _, a := range members.Applications {
		targets = append(targets, target{a.UUID, a.Name, "applications", "application"})
	}
	for _, d := range members.Databases {
		targets = append(targets, target{d.UUID, d.Name, "databases", "database"})
	}
	if len(targets) == 0 {
		return nil, guard.NewError(guard.CodeNotFound,
			"service "+uuid+" reports no containers to read logs from")
	}

	if container = strings.TrimSpace(container); container != "" {
		matched := targets[:0:0]
		for _, t := range targets {
			if strings.EqualFold(t.name, container) || t.uuid == container {
				matched = append(matched, t)
			}
		}
		if len(matched) == 0 {
			names := make([]string, 0, len(targets))
			for _, t := range targets {
				names = append(names, t.name)
			}
			return nil, guard.NewErrorWithRemedy(guard.CodeNotFound,
				"service "+uuid+" has no container named "+container,
				"containers in this service: "+strings.Join(names, ", "))
		}
		targets = matched
	}

	out := make([]ContainerLogs, 0, len(targets))
	for _, t := range targets {
		entry := ContainerLogs{Name: t.name, UUID: t.uuid, Kind: t.kind}
		// One unreadable container must not hide the others' logs, which are
		// often exactly where the failure shows up.
		raw, err := c.GetRaw(ctx, "/services/"+uuid+"/"+t.segment+"/"+t.uuid+"/logs", q)
		if err != nil {
			entry.Err = err.Error()
		} else {
			entry.Logs = raw
		}
		out = append(out, entry)
	}
	return out, nil
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
