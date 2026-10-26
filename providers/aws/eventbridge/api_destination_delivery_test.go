package eventbridge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// receivedRequest is what a test endpoint saw.
type receivedRequest struct {
	method string
	path   string
	query  map[string][]string
	header http.Header
	body   string
}

// recordingEndpoint is an httptest server that answers each call with the next
// status in statuses (repeating the last) and records every request.
type recordingEndpoint struct {
	*httptest.Server

	mu       sync.Mutex
	got      []receivedRequest
	statuses []int
	headers  map[string]string
}

func newRecordingEndpoint(t *testing.T, statuses ...int) *recordingEndpoint {
	t.Helper()

	e := &recordingEndpoint{statuses: statuses}
	e.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		e.mu.Lock()
		e.got = append(e.got, receivedRequest{
			method: r.Method, path: r.URL.Path, query: r.URL.Query(), header: r.Header.Clone(), body: string(body),
		})
		status := http.StatusOK
		if n := len(e.got); len(e.statuses) > 0 {
			status = e.statuses[min(n, len(e.statuses))-1]
		}

		for k, v := range e.headers {
			w.Header().Set(k, v)
		}
		e.mu.Unlock()

		w.WriteHeader(status)
	}))
	t.Cleanup(e.Close)

	return e
}

func (e *recordingEndpoint) requests() []receivedRequest {
	e.mu.Lock()
	defer e.mu.Unlock()

	return append([]receivedRequest(nil), e.got...)
}

// deliveryFixture wires a mock with a connection, an API destination to
// endpoint, and a rule on the default bus whose single target is that
// destination.
type deliveryFixture struct {
	m   *Mock
	sqs *recordingSQS
}

func newDeliveryFixture(t *testing.T, auth *driver.ConnectionConfig, endpoint, method string, target driver.Target) *deliveryFixture {
	t.Helper()

	m, _ := newConnectionMock(t)
	sqs := &recordingSQS{}
	m.SetSQSDeliverer(sqs)
	ctx := context.Background()

	c, err := m.CreateConnection(ctx, auth)
	require.NoError(t, err)

	d, err := m.CreateAPIDestination(ctx, &driver.APIDestinationConfig{
		Name: "dest", ConnectionARN: c.ARN, InvocationEndpoint: endpoint, HTTPMethod: method,
	})
	require.NoError(t, err)

	_, err = m.PutRule(ctx, &driver.RuleConfig{Name: "r", EventPattern: `{"source":["app"]}`})
	require.NoError(t, err)

	target.ID, target.ARN = "dest", d.ARN
	require.NoError(t, m.PutTargets(ctx, "", "r", []driver.Target{target}))

	return &deliveryFixture{m: m, sqs: sqs}
}

// put puts one event and waits for its background delivery to finish.
func (f *deliveryFixture) put(t *testing.T, detail string) {
	t.Helper()

	_, err := f.m.PutEvents(context.Background(), []driver.Event{{Source: "app", DetailType: "OrderCreated", Detail: detail}})
	require.NoError(t, err)

	f.settle(t)
}

// settle waits until no API destination delivery holds a slot.
func (f *deliveryFixture) settle(t *testing.T) {
	t.Helper()

	require.Eventually(t, func() bool { return len(f.m.apiDestSlots) == 0 }, 10*time.Second, 5*time.Millisecond)
}

func apiKeyConnection() *driver.ConnectionConfig {
	return &driver.ConnectionConfig{
		Name: "conn", AuthorizationType: "API_KEY",
		AuthParameters: &driver.ConnectionAuthParameters{
			APIKey: &driver.ConnectionAPIKeyAuth{APIKeyName: "x-api-key", APIKeyValue: "s3cr3t"},
			InvocationHTTPParameters: &driver.ConnectionHTTPParameters{
				HeaderParameters:      []driver.HTTPParameter{{Key: "x-shared", Value: "from-connection"}},
				QueryStringParameters: []driver.HTTPParameter{{Key: "shared", Value: "from-connection"}},
				BodyParameters:        []driver.HTTPParameter{{Key: "tenant", Value: "acme"}},
			},
		},
	}
}

func TestAPIDestinationDeliversWithAPIKey(t *testing.T) {
	e := newRecordingEndpoint(t)
	f := newDeliveryFixture(t, apiKeyConnection(), e.URL+"/orders/*/items/*", "POST", driver.Target{
		HTTPParameters: `{"PathParameterValues":["o-1","i 2"],` +
			`"HeaderParameters":{"x-shared":"from-target","x-target":"t","User-Agent":"spoofed"},` +
			`"QueryStringParameters":{"shared":"from-target","only":"target"}}`,
	})

	f.put(t, `{"id":"42"}`)

	got := e.requests()
	require.Len(t, got, 1)

	r := got[0]
	assert.Equal(t, "POST", r.method)
	assert.Equal(t, "/orders/o-1/items/i 2", r.path)
	assert.Equal(t, "s3cr3t", r.header.Get("x-api-key"))
	assert.Equal(t, "from-connection", r.header.Get("x-shared"), "connection headers win over target headers")
	assert.Equal(t, "t", r.header.Get("x-target"))
	assert.Equal(t, "Amazon/EventBridge/ApiDestinations", r.header.Get("User-Agent"), "User-Agent cannot be overridden")
	assert.Equal(t, "application/json; charset=utf-8", r.header.Get("Content-Type"))
	assert.Equal(t, "gzip,deflate", r.header.Get("Accept-Encoding"))
	assert.Equal(t, "close", r.header.Get("Connection"))
	assert.Equal(t, []string{"from-connection"}, r.query["shared"], "connection query parameters win")
	assert.Equal(t, []string{"target"}, r.query["only"])

	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(r.body), &body))
	assert.Equal(t, "OrderCreated", body["detail-type"])
	assert.Equal(t, "acme", body["tenant"], "connection body parameters are merged into the event")
}

func TestAPIDestinationBasicAuthAndInputTransformer(t *testing.T) {
	e := newRecordingEndpoint(t)
	f := newDeliveryFixture(t, &driver.ConnectionConfig{
		Name: "conn", AuthorizationType: "BASIC",
		AuthParameters: &driver.ConnectionAuthParameters{Basic: &driver.ConnectionBasicAuth{Username: "user", Password: "pass"}},
	}, e.URL+"/hook", "PUT", driver.Target{
		InputTransformer: `{"InputPathsMap":{"id":"$.detail.id"},"InputTemplate":"{\"order\": <id>}"}`,
	})

	f.put(t, `{"id":"42"}`)

	got := e.requests()
	require.Len(t, got, 1)

	user, pass, ok := (&http.Request{Header: got[0].header}).BasicAuth()
	assert.True(t, ok)
	assert.Equal(t, "user", user)
	assert.Equal(t, "pass", pass)
	assert.Equal(t, "PUT", got[0].method)
	assert.JSONEq(t, `{"order":"42"}`, got[0].body)
}

// oauthServer is a fake token endpoint that issues token-1, token-2, ... and
// counts how often it is called.
func oauthServer(t *testing.T, status int) (*httptest.Server, *atomic.Int32) {
	t.Helper()

	var calls atomic.Int32

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)

		id, secret, ok := r.BasicAuth()
		_ = r.ParseForm()

		if !ok || id != "client" || secret != "csecret" || r.PostForm.Get("grant_type") != "client_credentials" ||
			r.PostForm.Get("scope") != "events" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"access_token":"token-`+string(rune('0'+n))+`","expires_in":3600}`)
	}))
	t.Cleanup(s.Close)

	return s, &calls
}

func oauthConnection(tokenURL string) *driver.ConnectionConfig {
	return &driver.ConnectionConfig{
		Name: "conn", AuthorizationType: "OAUTH_CLIENT_CREDENTIALS",
		AuthParameters: &driver.ConnectionAuthParameters{OAuth: &driver.ConnectionOAuth{
			AuthorizationEndpoint: tokenURL, HTTPMethod: "POST",
			ClientParameters: &driver.ConnectionOAuthClient{ClientID: "client", ClientSecret: "csecret"},
			OAuthHTTPParameters: &driver.ConnectionHTTPParameters{
				BodyParameters: []driver.HTTPParameter{{Key: "scope", Value: "events"}},
			},
		}},
	}
}

func TestAPIDestinationOAuthTokenCachedAndRefreshedOn401(t *testing.T) {
	tokens, calls := oauthServer(t, http.StatusOK)
	e := newRecordingEndpoint(t, http.StatusOK, http.StatusOK, http.StatusUnauthorized, http.StatusOK)
	f := newDeliveryFixture(t, oauthConnection(tokens.URL), e.URL+"/hook", "POST", driver.Target{})

	f.put(t, `{}`)
	f.put(t, `{}`)
	assert.Equal(t, int32(1), calls.Load(), "the token is cached between calls")

	// The third call is rejected with 401: the token is dropped, a new one is
	// fetched, and the retry succeeds.
	f.put(t, `{}`)

	got := e.requests()
	require.Len(t, got, 4)
	assert.Equal(t, "Bearer token-1", got[0].header.Get("Authorization"))
	assert.Equal(t, "Bearer token-1", got[2].header.Get("Authorization"))
	assert.Equal(t, "Bearer token-2", got[3].header.Get("Authorization"))
	assert.Equal(t, int32(2), calls.Load())
	assert.Empty(t, f.sqs.got(queueA))
}

func TestAPIDestinationOAuthFailureDeauthorizes(t *testing.T) {
	tokens, _ := oauthServer(t, http.StatusInternalServerError)
	e := newRecordingEndpoint(t)
	f := newDeliveryFixture(t, oauthConnection(tokens.URL), e.URL+"/hook", "POST", driver.Target{
		DeadLetterConfig: `{"Arn":"` + queueA + `"}`,
	})

	f.put(t, `{}`)

	assert.Empty(t, e.requests(), "no call is made without a token")
	assert.Len(t, f.sqs.got(queueA), 1, "the event goes to the dead-letter queue")

	c, err := f.m.DescribeConnection(context.Background(), "conn")
	require.NoError(t, err)
	assert.Equal(t, "DEAUTHORIZED", c.State)
	assert.NotEmpty(t, c.StateReason)

	d, err := f.m.DescribeAPIDestination(context.Background(), "dest")
	require.NoError(t, err)
	assert.Equal(t, "INACTIVE", d.State)
}

func TestAPIDestinationRetriesThenDeadLetters(t *testing.T) {
	tests := []struct {
		name     string
		statuses []int
		policy   string
		calls    int
		dlq      bool
	}{
		{name: "5xx retried up to MaximumRetryAttempts", statuses: []int{500}, policy: `{"MaximumRetryAttempts":2}`, calls: 3, dlq: true},
		{name: "429 then success", statuses: []int{429, 200}, policy: `{"MaximumRetryAttempts":2}`, calls: 2},
		{name: "4xx not retried", statuses: []int{400}, policy: `{"MaximumRetryAttempts":5}`, calls: 1, dlq: true},
		{name: "3xx not retried", statuses: []int{302}, policy: `{"MaximumRetryAttempts":5}`, calls: 1, dlq: true},
		{name: "no retries", statuses: []int{503}, policy: `{"MaximumRetryAttempts":0}`, calls: 1, dlq: true},
		{name: "default policy", statuses: []int{500}, calls: 1 + defaultAPIDestinationRetries, dlq: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newRecordingEndpoint(t, tc.statuses...)
			f := newDeliveryFixture(t, apiKeyConnection(), e.URL, "POST", driver.Target{
				RetryPolicy: tc.policy, DeadLetterConfig: `{"Arn":"` + queueA + `"}`,
			})

			f.put(t, `{"n":1}`)

			assert.Len(t, e.requests(), tc.calls)

			if tc.dlq {
				dlq := f.sqs.got(queueA)
				require.Len(t, dlq, 1)
				assert.Equal(t, "OrderCreated", dlq[0]["detail-type"], "the DLQ gets the original event")
			} else {
				assert.Empty(t, f.sqs.got(queueA))
			}
		})
	}
}

func TestAPIDestinationNegativeRetryAfterStops(t *testing.T) {
	e := newRecordingEndpoint(t, http.StatusServiceUnavailable)
	e.headers = map[string]string{"Retry-After": "-1"}

	f := newDeliveryFixture(t, apiKeyConnection(), e.URL, "POST", driver.Target{RetryPolicy: `{"MaximumRetryAttempts":5}`})

	f.put(t, `{}`)
	assert.Len(t, e.requests(), 1)
}

func TestAPIDestinationTimeoutIsRetried(t *testing.T) {
	var calls atomic.Int32

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(slow.Close)

	f := newDeliveryFixture(t, apiKeyConnection(), slow.URL, "POST", driver.Target{
		RetryPolicy: `{"MaximumRetryAttempts":1}`, DeadLetterConfig: `{"Arn":"` + queueA + `"}`,
	})
	f.m.SetHTTPClient(&http.Client{Timeout: 50 * time.Millisecond})

	f.put(t, `{}`)

	assert.Equal(t, int32(2), calls.Load(), "a timed-out call is retried")
	assert.Len(t, f.sqs.got(queueA), 1)
}

func TestAPIDestinationInactiveIsNotCalled(t *testing.T) {
	e := newRecordingEndpoint(t)
	f := newDeliveryFixture(t, apiKeyConnection(), e.URL, "POST", driver.Target{DeadLetterConfig: `{"Arn":"` + queueA + `"}`})

	_, err := f.m.DeauthorizeConnection(context.Background(), "conn")
	require.NoError(t, err)

	f.put(t, `{}`)

	assert.Empty(t, e.requests())
	assert.Len(t, f.sqs.got(queueA), 1)
}

// TestAPIDestinationConcurrentDelivery puts events from several goroutines to an
// OAuth destination so the race detector checks the token cache and limiter.
func TestAPIDestinationConcurrentDelivery(t *testing.T) {
	tokens, _ := oauthServer(t, http.StatusOK)
	e := newRecordingEndpoint(t)
	f := newDeliveryFixture(t, oauthConnection(tokens.URL), e.URL, "POST", driver.Target{})

	var wg sync.WaitGroup

	for range 6 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_, err := f.m.PutEvents(context.Background(), []driver.Event{{Source: "app", DetailType: "OrderCreated", Detail: `{}`}})
			assert.NoError(t, err)
		}()
	}

	wg.Wait()
	f.settle(t)

	assert.Len(t, e.requests(), 6)
}

func TestRateLimiterSpacesCalls(t *testing.T) {
	var l rateLimiter

	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	assert.Zero(t, l.reserve("d", 2, now))
	assert.Zero(t, l.reserve("d", 2, now.Add(100*time.Millisecond)))
	assert.Equal(t, 800*time.Millisecond, l.reserve("d", 2, now.Add(200*time.Millisecond)), "the third call waits for the next window")
	assert.Equal(t, 800*time.Millisecond, l.reserve("d", 2, now.Add(200*time.Millisecond)), "and so does the fourth")
	assert.Equal(t, 1800*time.Millisecond, l.reserve("d", 2, now.Add(200*time.Millisecond)), "the fifth waits a window more")
	assert.Zero(t, l.reserve("other", 1, now), "destinations are limited separately")
	assert.Zero(t, l.reserve("d", 0, now), "no limit")
}

func TestAPIDestinationRateLimitDelaysCalls(t *testing.T) {
	e := newRecordingEndpoint(t)
	f := newDeliveryFixture(t, apiKeyConnection(), e.URL, "POST", driver.Target{})

	rate := 1
	_, err := f.m.UpdateAPIDestination(context.Background(), &driver.APIDestinationUpdate{Name: "dest", InvocationRateLimitPerSecond: &rate})
	require.NoError(t, err)

	start := time.Now()

	f.put(t, `{}`)
	f.put(t, `{}`)

	assert.Len(t, e.requests(), 2)
	assert.GreaterOrEqual(t, time.Since(start), 900*time.Millisecond, "the second call waits for the next one-second window")
}

// TestAPIDestinationDeliveryDoesNotBlockPutEvents checks PutEvents returns at
// once while a slow endpoint is still answering, and the endpoint still gets
// the event.
func TestAPIDestinationDeliveryDoesNotBlockPutEvents(t *testing.T) {
	var calls atomic.Int32

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second)
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(slow.Close)

	f := newDeliveryFixture(t, apiKeyConnection(), slow.URL, "POST", driver.Target{})

	start := time.Now()
	_, err := f.m.PutEvents(context.Background(), []driver.Event{{Source: "app", DetailType: "OrderCreated", Detail: `{}`}})
	require.NoError(t, err)
	assert.Less(t, time.Since(start), 200*time.Millisecond, "PutEvents does not wait for the endpoint")

	require.Eventually(t, func() bool { return calls.Load() == 1 }, 10*time.Second, 10*time.Millisecond)
	f.settle(t)
}

func TestRetryDelayCapsRetryAfter(t *testing.T) {
	assert.Equal(t, retryBaseDelay, retryDelay(0, 0))
	assert.Equal(t, retryMaxDelay, retryDelay(10, 0), "the backoff is capped")
	assert.Equal(t, 3*time.Second, retryDelay(0, 3*time.Second), "a longer Retry-After wins")
	assert.Equal(t, maxRetryAfter, retryDelay(0, 30*time.Second), "Retry-After is capped")
}

func TestRetryLimitsCapTheRetryWindow(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	retries, deadline := retryLimits("", start)
	assert.Equal(t, defaultAPIDestinationRetries, retries)
	assert.Equal(t, start.Add(maxRetryWindow), deadline, "the 24-hour default is capped")

	_, deadline = retryLimits(`{"MaximumEventAgeInSeconds":10}`, start)
	assert.Equal(t, start.Add(10*time.Second), deadline, "a shorter event age is kept")
}
