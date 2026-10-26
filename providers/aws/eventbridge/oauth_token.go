package eventbridge

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
)

// errOAuthFailed marks a call that could not get an OAuth token. EventBridge
// deauthorizes the connection, so the call is not retried.
var errOAuthFailed = stderrors.New("oauth token request failed")

var (
	errTokenStatus   = stderrors.New("authorization endpoint returned an error status")
	errNoAccessToken = stderrors.New("authorization endpoint returned no access_token")
)

// tokenCache holds the OAuth access token of each connection, keyed by the
// connection ARN, until it expires or the endpoint rejects it. It is not
// persisted: a restarted emulator fetches a new token on first use.
type tokenCache struct {
	mu     sync.Mutex
	tokens map[string]cachedToken
}

type cachedToken struct {
	value   string
	expires time.Time // zero means it does not expire
}

func (c *tokenCache) get(key string, now time.Time) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	t, ok := c.tokens[key]
	if !ok || (!t.expires.IsZero() && !now.Before(t.expires)) {
		return "", false
	}

	return t.value, true
}

func (c *tokenCache) put(key string, t cachedToken) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.tokens == nil {
		c.tokens = map[string]cachedToken{}
	}

	c.tokens[key] = t
}

// forget drops a connection's token so the next call fetches a new one, which
// is what EventBridge does when an endpoint answers 401 or 407.
func (c *tokenCache) forget(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.tokens, key)
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

// oauthToken returns the connection's cached access token, fetching one with
// the client credentials grant when there is none. A failed fetch deauthorizes
// the connection. No lock is held while the token endpoint is called.
func (m *Mock) oauthToken(ctx context.Context, conn *driver.Connection) (string, error) {
	if token, ok := m.tokens.get(conn.ARN, time.Now()); ok {
		return token, nil
	}

	tok, err := m.fetchOAuthToken(ctx, conn.AuthParameters.OAuth)
	if err != nil {
		m.deauthorizeAfterFailure(conn.Name, err)

		return "", fmt.Errorf("%w: %w", errOAuthFailed, err)
	}

	m.tokens.put(conn.ARN, tok)

	return tok.value, nil
}

// fetchOAuthToken calls the authorization endpoint with the client credentials
// (as HTTP Basic auth) and grant_type=client_credentials, plus the connection's
// OAuth HTTP parameters, and reads the access token from the JSON reply.
func (m *Mock) fetchOAuthToken(ctx context.Context, o *driver.ConnectionOAuth) (cachedToken, error) {
	req, err := buildTokenRequest(ctx, o)
	if err != nil {
		return cachedToken{}, err
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return cachedToken{}, err
	}

	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return cachedToken{}, err
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return cachedToken{}, fmt.Errorf("%w: %d", errTokenStatus, resp.StatusCode)
	}

	var tr tokenResponse
	if err := json.Unmarshal(data, &tr); err != nil || tr.AccessToken == "" {
		return cachedToken{}, errNoAccessToken
	}

	tok := cachedToken{value: tr.AccessToken}
	if tr.ExpiresIn > 0 {
		tok.expires = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	}

	return tok, nil
}

// tokenParams returns the form and query of a token request: the client
// credentials grant plus the connection's OAuth body and query parameters.
func tokenParams(o *driver.ConnectionOAuth, query url.Values) url.Values {
	form := url.Values{"grant_type": {"client_credentials"}}

	if p := o.OAuthHTTPParameters; p != nil {
		for _, b := range p.BodyParameters {
			form.Set(b.Key, b.Value)
		}

		for _, q := range p.QueryStringParameters {
			query.Set(q.Key, q.Value)
		}
	}

	return form
}

func buildTokenRequest(ctx context.Context, o *driver.ConnectionOAuth) (*http.Request, error) {
	endpoint, err := url.Parse(o.AuthorizationEndpoint)
	if err != nil {
		return nil, err
	}

	query := endpoint.Query()
	form := tokenParams(o, query)

	var body io.Reader

	if o.HTTPMethod == http.MethodGet {
		for k, v := range form {
			query[k] = v
		}
	} else {
		body = strings.NewReader(form.Encode())
	}

	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, o.HTTPMethod, endpoint.String(), body)
	if err != nil {
		return nil, err
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	setTokenHeaders(req, o)

	return req, nil
}

// setTokenHeaders adds the connection's OAuth headers and the client
// credentials, sent as HTTP Basic auth.
func setTokenHeaders(req *http.Request, o *driver.ConnectionOAuth) {
	if p := o.OAuthHTTPParameters; p != nil {
		for _, h := range p.HeaderParameters {
			req.Header.Set(h.Key, h.Value)
		}
	}

	if c := o.ClientParameters; c != nil {
		req.SetBasicAuth(c.ClientID, c.ClientSecret)
	}
}

// deauthorizeAfterFailure moves a connection whose token request failed to
// DEAUTHORIZED, recording why, as EventBridge does. Its API destinations then
// read INACTIVE.
func (m *Mock) deauthorizeAfterFailure(name string, cause error) {
	m.connections.Update(name, func(c connectionData) connectionData {
		c.Info.State = connStateDeauthorized
		c.Info.StateReason = "Failed to authorize with the authorization endpoint: " + cause.Error()

		return c
	})
}
