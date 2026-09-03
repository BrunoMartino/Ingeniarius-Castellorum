package coolify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"coolify-mcp/internal/guard"
)

const apiPrefix = "/api/v1"

// maxErrorBody caps how much of an upstream error body is echoed back, so a
// verbose Coolify error can never flood the agent's context.
const maxErrorBody = 400

// Client is the only thing in this MCP that talks to Coolify. Every request
// passes through RoutePolicy first (A7): a badly written tool cannot reach a
// route the policy does not list, and DELETE never leaves the machine.
type Client struct {
	baseURL string
	token   string
	user    string
	policy  *guard.RoutePolicy
	audit   *guard.Auditor
	http    *http.Client
	cache   *inventoryCache
}

func NewClient(baseURL, token, user string, policy *guard.RoutePolicy, audit *guard.Auditor, httpClient *http.Client) *Client {
	if policy == nil {
		policy = guard.NewRoutePolicy()
	}
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		user:    user,
		policy:  policy,
		audit:   audit,
		http:    httpClient,
		cache:   newInventoryCache(),
	}
}

func (c *Client) User() string { return c.user }

// Get performs an allowlisted GET and decodes the JSON body into out.
func (c *Client) Get(ctx context.Context, path string, query url.Values, out any) error {
	return c.do(ctx, http.MethodGet, path, query, nil, out)
}

// Post performs an allowlisted POST with an optional JSON body.
func (c *Client) Post(ctx context.Context, path string, query url.Values, body any, out any) error {
	return c.do(ctx, http.MethodPost, path, query, body, out)
}

// Patch performs an allowlisted PATCH with a JSON body.
func (c *Client) Patch(ctx context.Context, path string, body any, out any) error {
	return c.do(ctx, http.MethodPatch, path, nil, body, out)
}

// GetRaw returns the undecoded body, for endpoints whose shape varies by
// resource kind (database detail, logs).
func (c *Client) GetRaw(ctx context.Context, path string, query url.Values) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, path, query, nil, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	if err := c.policy.AssertRequest(method, path); err != nil {
		c.audit.Record(c.user, "http", "", method, path, err)
		return err
	}

	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return guard.NewError(guard.CodeBadInput, "request body is not JSON-encodable: "+err.Error())
		}
		payload = bytes.NewReader(encoded)
	}

	full := c.baseURL + apiPrefix + path
	if len(query) > 0 {
		full += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, full, payload)
	if err != nil {
		return guard.NewError(guard.CodeBadInput, err.Error())
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		// The URL can carry a query string but never the token, which lives in
		// a header — so err is safe to surface.
		return guard.NewError(guard.CodeUpstream, "request to Coolify failed: "+scrub(err.Error(), c.token))
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return guard.NewError(guard.CodeUpstream, "reading Coolify response failed: "+err.Error())
	}

	if resp.StatusCode == http.StatusNotFound {
		return guard.NewError(guard.CodeNotFound, fmt.Sprintf("Coolify returned 404 for %s %s", method, path))
	}
	if resp.StatusCode >= 400 {
		return guard.NewError(guard.CodeUpstream,
			fmt.Sprintf("Coolify returned %d for %s %s: %s", resp.StatusCode, method, path, snippet(raw)))
	}

	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return guard.NewError(guard.CodeUpstream,
			fmt.Sprintf("could not decode Coolify response for %s %s: %s", method, path, err.Error()))
	}
	return nil
}

func snippet(raw []byte) string {
	s := strings.TrimSpace(string(raw))
	if len(s) > maxErrorBody {
		return s[:maxErrorBody] + "…"
	}
	return s
}

func scrub(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "[redacted]")
}

// IsNotFound reports whether err is a Coolify 404, used when probing which
// kind a uuid belongs to.
func IsNotFound(err error) bool {
	var ge guard.Error
	return errors.As(err, &ge) && ge.Code == guard.CodeNotFound
}
