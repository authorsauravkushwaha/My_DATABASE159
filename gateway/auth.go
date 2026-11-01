package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type collectionSchema struct {
	Type       string `json:"type"`
	Properties map[string]struct {
		Type string `json:"type"`
	} `json:"properties"`
	Required             []string `json:"required"`
	AdditionalProperties bool     `json:"additionalProperties"`
}

type accessGrant struct {
	ProjectID    string           `json:"project_id"`
	CollectionID string           `json:"collection_id"`
	Name         string           `json:"name"`
	Schema       collectionSchema `json:"schema"`
}

// authorizer delegates BOTH authentication and membership to the control plane.
// A valid signature or a namespaced key alone is never sufficient authorization.
type authorizer struct {
	baseURL string
	client  *http.Client
}

func newAuthorizer(baseURL string) (*authorizer, error) {
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("CONTROL_PLANE_URL must be an HTTP(S) URL without credentials, query, or fragment")
	}
	return &authorizer{baseURL: strings.TrimRight(baseURL, "/"), client: &http.Client{
		Timeout:       8 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (a *authorizer) authorize(ctx context.Context, bearer, project, collection string, write bool) (accessGrant, int, error) {
	var grant accessGrant
	if bearer == "" || len(bearer) > 8192 {
		return grant, http.StatusUnauthorized, errors.New("bearer token required")
	}
	query := url.Values{"write": []string{fmt.Sprint(write)}}
	endpoint := a.baseURL + "/v1/projects/" + url.PathEscape(project) + "/collections/" + url.PathEscape(collection) + "/access?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return grant, http.StatusServiceUnavailable, err
	}
	req.Header.Set("Authorization", bearer)
	req.Header.Set("Accept", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return grant, http.StatusServiceUnavailable, errors.New("authorization service unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusUnprocessableEntity {
			return grant, resp.StatusCode, errors.New("access denied")
		}
		return grant, http.StatusServiceUnavailable, errors.New("authorization service unavailable")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32769))
	if err != nil || len(body) > 32768 {
		return grant, http.StatusServiceUnavailable, errors.New("invalid authorization response")
	}
	if err := json.Unmarshal(body, &grant); err != nil || grant.ProjectID != project || grant.CollectionID != collection || grant.Schema.Type != "object" || grant.Schema.AdditionalProperties || grant.Schema.Properties == nil {
		return grant, http.StatusServiceUnavailable, errors.New("invalid authorization response")
	}
	return grant, http.StatusOK, nil
}
