package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// maxGQLResponse caps how much of a GraphQL response body we read, a backstop
// against a pathological response.
const maxGQLResponse = 32 << 20 // 32 MiB

// gqlClient is a minimal GraphQL client for GitLab's /api/graphql endpoint. glute
// uses REST (client-go) for almost everything; GraphQL is used only where REST
// can't reach — currently a job's needs: dependencies. It's hand-rolled to avoid
// a new module dependency and keep cross-compilation trivial.
type gqlClient struct {
	http     *http.Client
	endpoint string
	token    string
}

// newGQLClient builds a GraphQL client for the instance at baseURL. httpClient
// may be nil (the default client is used), and should be the same CA-aware
// client the REST side uses so self-managed CAs keep working.
func newGQLClient(baseURL, token string, httpClient *http.Client) *gqlClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &gqlClient{
		http:     httpClient,
		endpoint: strings.TrimRight(baseURL, "/") + "/api/graphql",
		token:    token,
	}
}

// query runs a GraphQL query with the given variables and unmarshals the "data"
// field into out. It errors on transport failure, a non-200 status, or a
// non-empty GraphQL errors array (so callers can retry rather than act on
// partial data).
func (c *gqlClient) query(ctx context.Context, query string, vars map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return fmt.Errorf("gql marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("gql request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("gql post: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxGQLResponse))
	if err != nil {
		return fmt.Errorf("gql read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("gql http %d: %s", resp.StatusCode, snippet(raw))
	}

	var env struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("gql decode: %w", err)
	}
	if len(env.Errors) > 0 {
		msgs := make([]string, 0, len(env.Errors))
		for _, e := range env.Errors {
			msgs = append(msgs, e.Message)
		}
		return fmt.Errorf("gql errors: %s", strings.Join(msgs, "; "))
	}
	if out != nil {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("gql decode data: %w", err)
		}
	}
	return nil
}

// snippet trims a response body for inclusion in an error message.
func snippet(b []byte) string {
	const n = 200
	if len(b) > n {
		return string(b[:n]) + "…"
	}
	return string(b)
}
