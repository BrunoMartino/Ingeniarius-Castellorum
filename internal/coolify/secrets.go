package coolify

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

// EnvVar is one environment variable. Value is omitted entirely by
// ListEnvKeys and masked by default by GetEnvValues.
type EnvVar struct {
	UUID        string `json:"uuid,omitempty"`
	Key         string `json:"key"`
	Value       string `json:"value,omitempty"`
	Masked      bool   `json:"masked,omitempty"`
	IsBuildTime bool   `json:"is_build_time,omitempty"`
	IsLiteral   bool   `json:"is_literal,omitempty"`
	IsPreview   bool   `json:"is_preview,omitempty"`
	IsShownOnce bool   `json:"is_shown_once,omitempty"`
}

type rawEnvVar struct {
	UUID        string `json:"uuid"`
	Key         string `json:"key"`
	Value       string `json:"value"`
	IsBuildTime bool   `json:"is_build_time"`
	IsLiteral   bool   `json:"is_literal"`
	IsPreview   bool   `json:"is_preview"`
	IsShownOnce bool   `json:"is_shown_once"`
}

func (c *Client) fetchEnvs(ctx context.Context, uuid string) (Resource, []rawEnvVar, error) {
	res, err := c.Resolve(ctx, uuid)
	if err != nil {
		return Resource{}, nil, err
	}
	var envs []rawEnvVar
	if err := c.Get(ctx, res.Kind.segment()+"/"+uuid+"/envs", nil, &envs); err != nil {
		return res, nil, err
	}
	sort.Slice(envs, func(i, j int) bool { return envs[i].Key < envs[j].Key })
	return res, envs, nil
}

// ListEnvKeys returns names only. It never touches a value, so it needs no
// read:sensitive scope and produces no sensitive audit entry.
func (c *Client) ListEnvKeys(ctx context.Context, uuid string) ([]string, error) {
	_, envs, err := c.fetchEnvs(ctx, uuid)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(envs))
	for _, e := range envs {
		keys = append(keys, e.Key)
	}
	return keys, nil
}

// GetEnvValues returns the variables of a resource. mask=true (the default at
// the tool boundary) replaces every value with a length hint, so the agent can
// reason about which variables are set without the secrets entering context.
func (c *Client) GetEnvValues(ctx context.Context, uuid string, mask bool) ([]EnvVar, error) {
	_, envs, err := c.fetchEnvs(ctx, uuid)
	if err != nil {
		return nil, err
	}
	out := make([]EnvVar, 0, len(envs))
	for _, e := range envs {
		v := EnvVar{
			UUID:        e.UUID,
			Key:         e.Key,
			Value:       e.Value,
			IsBuildTime: e.IsBuildTime,
			IsLiteral:   e.IsLiteral,
			IsPreview:   e.IsPreview,
			IsShownOnce: e.IsShownOnce,
		}
		if mask {
			v.Value = MaskValue(e.Value)
			v.Masked = true
		}
		out = append(out, v)
	}
	return out, nil
}

// MaskValue hides a secret while keeping its length, which is enough to tell an
// empty variable from a set one and to spot an accidentally truncated value.
func MaskValue(v string) string {
	if v == "" {
		return ""
	}
	return "***(" + strconv.Itoa(len(v)) + " chars)"
}

// credentialKeys are the fields of a managed database record that carry
// connection material. Anything outside this list is not returned, so a future
// Coolify field cannot leak through by accident.
var credentialKeys = map[string]bool{
	"internal_db_url": true, "external_db_url": true,
	"public_port": true, "is_public": true, "image": true,
	"postgres_user": true, "postgres_password": true, "postgres_db": true,
	"mysql_user": true, "mysql_password": true, "mysql_root_password": true, "mysql_database": true,
	"mariadb_user": true, "mariadb_password": true, "mariadb_root_password": true, "mariadb_database": true,
	"mongo_initdb_root_username": true, "mongo_initdb_root_password": true, "mongo_initdb_database": true,
	"redis_password": true, "redis_username": true,
	"keydb_password":        true,
	"dragonfly_password":    true,
	"clickhouse_admin_user": true, "clickhouse_admin_password": true,
}

// secretKeys are the subset of credentialKeys whose value is a secret and is
// therefore masked when mask=true.
var secretKeys = map[string]bool{
	"internal_db_url": true, "external_db_url": true,
	"postgres_password": true,
	"mysql_password":    true, "mysql_root_password": true,
	"mariadb_password": true, "mariadb_root_password": true,
	"mongo_initdb_root_password": true,
	"redis_password":             true,
	"keydb_password":             true,
	"dragonfly_password":         true,
	"clickhouse_admin_password":  true,
}

// DatabaseCredentials is the credential projection of a managed database.
type DatabaseCredentials struct {
	UUID        string         `json:"uuid"`
	Name        string         `json:"name,omitempty"`
	Engine      string         `json:"engine,omitempty"`
	Status      string         `json:"status,omitempty"`
	Masked      bool           `json:"masked"`
	Credentials map[string]any `json:"credentials"`
}

// GetDatabaseCredentials returns only the connection fields of a managed
// database, masked by default. Applications and services are refused: their
// secrets live in environment variables, which get_env_values covers.
func (c *Client) GetDatabaseCredentials(ctx context.Context, uuid string, mask bool) (*DatabaseCredentials, error) {
	res, raw, err := c.Detail(ctx, uuid)
	if err != nil {
		return nil, err
	}
	if res.Kind != KindDatabase {
		return nil, notADatabase(uuid, res.Kind)
	}
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, err
	}
	creds := make(map[string]any, len(credentialKeys))
	for key, value := range record {
		if !credentialKeys[key] {
			continue
		}
		if mask && secretKeys[key] {
			if s, ok := value.(string); ok {
				creds[key] = MaskValue(s)
				continue
			}
		}
		creds[key] = value
	}
	return &DatabaseCredentials{
		UUID:        res.UUID,
		Name:        res.Name,
		Engine:      strings.TrimPrefix(res.RawType, "standalone-"),
		Status:      res.Status,
		Masked:      mask,
		Credentials: creds,
	}, nil
}
