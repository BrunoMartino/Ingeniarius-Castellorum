package coolify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptrace"
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
	deploys *deployTracker
	logger  *slog.Logger
}

func NewClient(baseURL, token, user string, policy *guard.RoutePolicy, audit *guard.Auditor, httpClient *http.Client, logger *slog.Logger) *Client {
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
		deploys: newDeployTracker(),
		logger:  logger,
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

	var localAddr, remoteAddr string
	trace := &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			if info.Conn == nil {
				return
			}
			localAddr = info.Conn.LocalAddr().String()
			remoteAddr = info.Conn.RemoteAddr().String()
		},
	}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), method, full, payload)
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
		return c.statusError(method, full, localAddr, remoteAddr, resp, raw)
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

func (c *Client) statusError(method, fullURL, localAddr, remoteAddr string, resp *http.Response, raw []byte) error {
	body := snippet(raw)
	tokenLen, tokenPrefix := tokenFingerprint(c.token)
	headers := interestingHeaders(resp.Header)
	c.log(slog.LevelWarn, "coolify upstream error",
		"method", method,
		"url", fullURL,
		"status", resp.StatusCode,
		"body", body,
		"token_len", tokenLen,
		"token_prefix", tokenPrefix,
		"tcp_local", localAddr,
		"tcp_remote", remoteAddr,
		"response_headers", headers,
	)
	reason := fmt.Sprintf(
		"Coolify returned %d for %s %s | auth=Bearer token_len=%d token_prefix=%s | tcp local=%s remote=%s | body=%s",
		resp.StatusCode, method, fullURL, tokenLen, tokenPrefix, orDash(localAddr), orDash(remoteAddr), body,
	)
	if headers != "" {
		reason += " | headers=" + headers
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return guard.NewErrorWithRemedy(guard.CodeUpstream, reason, diagnoseCoolify(resp.StatusCode, body))
	}
	return guard.NewErrorWithRemedy(guard.CodeUpstream, reason, tokenTTLRemedy)
}

func (c *Client) log(level slog.Level, msg string, args ...any) {
	if c.logger == nil {
		return
	}
	c.logger.Log(context.Background(), level, msg, args...)
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

// tokenFingerprint is safe to log: length plus the Laravel "id|" prefix, never the secret.
func tokenFingerprint(token string) (int, string) {
	n := len(token)
	if n == 0 {
		return 0, "empty"
	}
	if i := strings.IndexByte(token, '|'); i >= 0 && i <= 6 {
		return n, token[:i+1] + "…"
	}
	return n, "…"
}

func interestingHeaders(h http.Header) string {
	keys := []string{"CF-Ray", "CF-Connecting-IP", "Server", "Via", "WWW-Authenticate", "X-Request-Id", "X-Forwarded-For"}
	var parts []string
	for _, k := range keys {
		if v := strings.TrimSpace(h.Get(k)); v != "" {
			parts = append(parts, k+"="+v)
		}
	}
	return strings.Join(parts, " ")
}

// tokenTTLRemedy is appended to every Coolify refusal: this MCP only accepts
// API tokens issued with a 7-day expiry.
const tokenTTLRemedy = "This MCP expects Coolify API tokens with a 7-day TTL. Ask the human to renew the token: Coolify → Security → API Tokens, create a new one expiring in 7 days (scopes read, read:sensitive, write, deploy; never root), set COOLIFY_API_TOKEN, and reload the MCP."

func diagnoseCoolify(status int, body string) string {
	lower := strings.ToLower(body)
	var specific string
	switch {
	case strings.Contains(lower, "you are not allowed to access the api"):
		specific = "Coolify rejected this client IP (ApiAllowed middleware), not the token and not COOLIFY_USER. $request->ip() on the Coolify host is compared to Settings → Advanced → Allowed IPs; behind Cloudflare/proxy that IP is often the proxy, not this machine's public address. Add that IP, or 0.0.0.0 to allow all, then Save."
	case strings.Contains(lower, "api is disabled"):
		specific = "Enable API Access in Coolify Settings → Advanced and Save."
	case status == http.StatusUnauthorized:
		specific = "Coolify rejected the bearer token. Check COOLIFY_API_TOKEN. COOLIFY_USER is audit-only and is not sent."
	default:
		specific = "Coolify refused the request. Confirm COOLIFY_URL points at the instance root (no /mcp), API Access is on, Allowed IPs includes the IP Coolify sees, and COOLIFY_API_TOKEN is valid. COOLIFY_USER is not sent."
	}
	return specific + " " + tokenTTLRemedy
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// IsNotFound reports whether err is a Coolify 404, used when probing which
// kind a uuid belongs to.
func IsNotFound(err error) bool {
	var ge guard.Error
	return errors.As(err, &ge) && ge.Code == guard.CodeNotFound
}
