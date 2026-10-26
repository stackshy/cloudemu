package eventbridge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
)

const (
	// apiDestinationTimeout is how long EventBridge waits for an API
	// destination to answer before it times the request out.
	apiDestinationTimeout = 5 * time.Second

	// defaultAPIDestinationRetries is how many times a failed API destination
	// call is retried when the target's RetryPolicy does not say. Real
	// EventBridge defaults to 185 attempts spread over 24 hours; the emulator
	// uses a small default and honors an explicit MaximumRetryAttempts as given.
	defaultAPIDestinationRetries = 2

	// maxRetryAfter caps how long an endpoint's Retry-After can delay the next
	// attempt, and maxRetryWindow caps how long one delivery keeps retrying, so
	// a background delivery always ends in bounded time.
	maxRetryAfter  = 5 * time.Second
	maxRetryWindow = 30 * time.Second

	// maxAPIDestinationDeliveries is how many API destination deliveries may
	// be in flight at once per provider. Deliveries past it wait for a slot.
	maxAPIDestinationDeliveries = 64

	// defaultMaxEventAge is how long EventBridge keeps retrying an event when
	// the RetryPolicy does not set MaximumEventAgeInSeconds.
	defaultMaxEventAge = 24 * time.Hour

	retryBaseDelay = 50 * time.Millisecond
	retryMaxDelay  = time.Second

	// maxResponseBody bounds how much of a response is read before it is
	// discarded.
	maxResponseBody = 1 << 20

	apiDestinationUserAgent = "Amazon/EventBridge/ApiDestinations"
	apiDestinationRange     = "bytes=0-1048575"
	apiDestinationEncoding  = "gzip,deflate"
	apiDestinationConn      = "close"
	defaultContentType      = "application/json; charset=utf-8"
)

// HTTPDoer is the outbound HTTP seam API destination targets are called
// through. The default is a client with EventBridge's 5-second timeout that does
// not follow redirects; callers can wire their own with SetHTTPClient.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// SetHTTPClient wires the client API destination targets call out with.
func (m *Mock) SetHTTPClient(c HTTPDoer) { m.httpClient = c }

func defaultHTTPClient() HTTPDoer {
	return &http.Client{
		Timeout:       apiDestinationTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// removedHeaders are the headers EventBridge strips from every API destination
// request, whatever the connection or target sets. User-Agent and Range are
// among them because EventBridge sets its own values.
func removedHeaders() map[string]bool {
	names := []string{
		"A-IM", "Accept-Charset", "Accept-Datetime", "Accept-Encoding", "Cache-Control", "Connection",
		"Content-Encoding", "Content-Length", "Content-MD5", "Date", "Expect", "Forwarded", "From", "Host",
		"HTTP2-Settings", "If-Match", "If-Modified-Since", "If-None-Match", "If-Range", "If-Unmodified-Since",
		"Max-Forwards", "Origin", "Pragma", "Proxy-Authorization", "Range", "Referer", "TE", "Trailer",
		"Transfer-Encoding", "User-Agent", "Upgrade", "Via", "Warning",
	}

	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[http.CanonicalHeaderKey(n)] = true
	}

	return out
}

// targetHTTPParameters is a target's HttpParameters block.
type targetHTTPParameters struct {
	PathParameterValues   []string          `json:"PathParameterValues"`
	HeaderParameters      map[string]string `json:"HeaderParameters"`
	QueryStringParameters map[string]string `json:"QueryStringParameters"`
}

// retryPolicy is a target's RetryPolicy block.
type retryPolicy struct {
	MaximumRetryAttempts     *int `json:"MaximumRetryAttempts"`
	MaximumEventAgeInSeconds *int `json:"MaximumEventAgeInSeconds"`
}

func parseHTTPParameters(raw string) targetHTTPParameters {
	var p targetHTTPParameters
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &p)
	}

	return p
}

// retryLimits returns how many retries a failed call gets and the wall-clock
// time after which no further retry is made: the RetryPolicy's event age, but
// never more than maxRetryWindow from start.
func retryLimits(raw string, start time.Time) (retries int, deadline time.Time) {
	var p retryPolicy
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &p)
	}

	retries = defaultAPIDestinationRetries
	if p.MaximumRetryAttempts != nil {
		retries = *p.MaximumRetryAttempts
	}

	age := defaultMaxEventAge
	if p.MaximumEventAgeInSeconds != nil {
		age = time.Duration(*p.MaximumEventAgeInSeconds) * time.Second
	}

	return retries, start.Add(min(age, maxRetryWindow))
}

// destinationCall is everything one API destination request is built from: a
// copy of the destination and its connection, taken so no store lock is held
// while the request is in flight.
type destinationCall struct {
	dest   driver.APIDestination
	conn   driver.Connection
	params targetHTTPParameters
	body   string
}

// resolveDestinationCall looks up the API destination a target ARN names and
// its connection. It fails when either is missing or the connection is not
// authorized, which leaves the destination INACTIVE.
func (m *Mock) resolveDestinationCall(t *driver.Target, body string) (*destinationCall, bool) {
	name := t.ARN[strings.Index(t.ARN, ":api-destination/")+len(":api-destination/"):]
	if slash := strings.Index(name, "/"); slash != -1 {
		name = name[:slash]
	}

	dest, ok := m.apiDestinations.Get(name)
	if !ok || !strings.HasPrefix(t.ARN, dest.ARN) {
		return nil, false
	}

	cd, ok := m.settleConnection(connectionNameFromARN(dest.ConnectionARN))
	if !ok || cd.Info.ARN != dest.ConnectionARN || cd.Info.State != connStateAuthorized {
		return nil, false
	}

	return &destinationCall{dest: dest, conn: cd.Info, params: parseHTTPParameters(t.HTTPParameters), body: body}, true
}

func isAPIDestination(arn string) bool {
	return strings.Contains(arn, ":api-destination/")
}

// goDeliverAPIDestination runs one API destination delivery in the background,
// as EventBridge delivers after PutEvents has returned. It takes a delivery
// slot when one is free; otherwise a goroutine waits for one, so the caller
// never blocks. Rate-limit waits, retries and the dead-letter send all happen
// in that goroutine, with no lock held. The provider has no shutdown hook, so a
// delivery still running when the process exits is dropped; each one ends
// within maxRetryWindow plus one request timeout.
func (m *Mock) goDeliverAPIDestination(deliver func()) {
	run := func() {
		defer func() { <-m.apiDestSlots }()

		deliver()
	}

	select {
	case m.apiDestSlots <- struct{}{}:
		go run()
	default:
		go func() {
			m.apiDestSlots <- struct{}{}

			run()
		}()
	}
}

// dispatchAPIDestination calls the HTTP endpoint of an API destination target,
// retrying per the target's RetryPolicy on the status codes EventBridge retries
// (401, 407, 409, 429 and 5xx) and on timeouts. It reports whether the call
// succeeded; a failure goes to the target's dead-letter queue.
func (m *Mock) dispatchAPIDestination(ctx context.Context, t *driver.Target, body string) bool {
	call, ok := m.resolveDestinationCall(t, body)
	if !ok {
		return false
	}

	retries, deadline := retryLimits(t.RetryPolicy, time.Now())

	for attempt := 0; ; attempt++ {
		delivered, retry, retryAfter := m.callOnce(ctx, call)
		if delivered {
			return true
		}

		if !retry || attempt >= retries {
			return false
		}

		delay := retryDelay(attempt, retryAfter)
		if time.Now().Add(delay).After(deadline) {
			return false
		}

		time.Sleep(delay)
	}
}

// callOnce makes one rate-limited call and reports whether it delivered the
// event, whether a failure may be retried, and the endpoint's Retry-After. A
// 401 or 407 drops the cached OAuth token so a retry fetches a fresh one.
func (m *Mock) callOnce(ctx context.Context, call *destinationCall) (delivered, retry bool, retryAfter time.Duration) {
	time.Sleep(m.limiter.reserve(call.dest.ARN, call.dest.InvocationRateLimitPerSecond, time.Now()))

	status, retryAfter, err := m.sendToDestination(ctx, call)
	if err == nil && status >= http.StatusOK && status < http.StatusMultipleChoices {
		return true, false, 0
	}

	if status == http.StatusUnauthorized || status == http.StatusProxyAuthRequired {
		m.tokens.forget(call.conn.ARN)
	}

	return false, shouldRetry(status, retryAfter, err), retryAfter
}

// shouldRetry reports whether a failed call may be retried: not when no OAuth
// token could be had, not when the endpoint sent a negative Retry-After, and
// otherwise only for the outcomes EventBridge retries.
func shouldRetry(status int, retryAfter time.Duration, err error) bool {
	if errors.Is(err, errOAuthFailed) || retryAfter < 0 {
		return false
	}

	return retryable(status, err)
}

// retryable reports whether EventBridge retries a call that ended with status
// (or with a transport error such as a timeout).
func retryable(status int, err error) bool {
	if err != nil {
		return true
	}

	switch status {
	case http.StatusUnauthorized, http.StatusProxyAuthRequired, http.StatusConflict, http.StatusTooManyRequests:
		return true
	default:
		return status >= http.StatusInternalServerError
	}
}

// retryDelay is the wait before retry number attempt+1: an exponential backoff
// capped at retryMaxDelay, or the endpoint's Retry-After (capped at
// maxRetryAfter) when that is longer, as EventBridge picks the more
// conservative of the two.
func retryDelay(attempt int, retryAfter time.Duration) time.Duration {
	const maxShift = 5

	backoff := min(retryBaseDelay<<min(attempt, maxShift), retryMaxDelay)

	return max(backoff, min(retryAfter, maxRetryAfter))
}

// sendToDestination makes one request and returns its status and Retry-After
// (negative when the endpoint asked for no retry, zero when it said nothing).
func (m *Mock) sendToDestination(ctx context.Context, call *destinationCall) (status int, retryAfter time.Duration, err error) {
	rctx, cancel := context.WithTimeout(ctx, apiDestinationTimeout)
	defer cancel()

	req, err := m.buildDestinationRequest(rctx, call)
	if err != nil {
		return 0, 0, err
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return 0, 0, err
	}

	defer resp.Body.Close()

	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBody))

	return resp.StatusCode, parseRetryAfter(resp.Header.Get("Retry-After")), nil
}

func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}

	secs, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0
	}

	return time.Duration(secs) * time.Second
}

// buildDestinationRequest renders the HTTP request for one call: the endpoint
// with "*" path parameters filled in, the target's then the connection's query
// and header parameters (the connection wins on a clash), the connection's
// authorization, and the shaped event as the body.
func (m *Mock) buildDestinationRequest(ctx context.Context, call *destinationCall) (*http.Request, error) {
	target, err := url.Parse(fillPathParameters(call.dest.InvocationEndpoint, call.params.PathParameterValues))
	if err != nil {
		return nil, err
	}

	inv := call.conn.AuthParameters.InvocationHTTPParameters

	query := target.Query()
	for k, v := range call.params.QueryStringParameters {
		query.Set(k, v)
	}

	if inv != nil {
		for _, p := range inv.QueryStringParameters {
			query.Set(p.Key, p.Value)
		}
	}

	target.RawQuery = query.Encode()

	var body io.Reader
	if call.dest.HTTPMethod != http.MethodGet && call.dest.HTTPMethod != http.MethodHead {
		body = strings.NewReader(mergeBodyParameters(call.body, inv))
	}

	req, err := http.NewRequestWithContext(ctx, call.dest.HTTPMethod, target.String(), body)
	if err != nil {
		return nil, err
	}

	applyHeaders(req.Header, call.params.HeaderParameters, inv)

	if err := m.authorizeRequest(ctx, req, &call.conn); err != nil {
		return nil, err
	}

	return req, nil
}

// fillPathParameters replaces each "*" in endpoint, in order, with the next
// PathParameterValues entry.
func fillPathParameters(endpoint string, values []string) string {
	for _, v := range values {
		idx := strings.Index(endpoint, "*")
		if idx == -1 {
			break
		}

		endpoint = endpoint[:idx] + url.PathEscape(v) + endpoint[idx+1:]
	}

	return endpoint
}

// applyHeaders sets the target's headers, then the connection's (which win on
// a clash), drops the headers EventBridge removes, and adds EventBridge's own
// fixed User-Agent, Range, Accept-Encoding and Connection headers.
func applyHeaders(h http.Header, target map[string]string, inv *driver.ConnectionHTTPParameters) {
	removed := removedHeaders()

	set := func(k, v string) {
		if !removed[http.CanonicalHeaderKey(k)] {
			h.Set(k, v)
		}
	}

	for k, v := range target {
		set(k, v)
	}

	if inv != nil {
		for _, p := range inv.HeaderParameters {
			set(p.Key, p.Value)
		}
	}

	if h.Get("Content-Type") == "" {
		h.Set("Content-Type", defaultContentType)
	}

	h.Set("User-Agent", apiDestinationUserAgent)
	h.Set("Range", apiDestinationRange)
	h.Set("Accept-Encoding", apiDestinationEncoding)
	h.Set("Connection", apiDestinationConn)
}

// mergeBodyParameters adds the connection's body parameters to a JSON object
// body. Any other body is sent as it is.
func mergeBodyParameters(body string, inv *driver.ConnectionHTTPParameters) string {
	if inv == nil || len(inv.BodyParameters) == 0 {
		return body
	}

	var obj map[string]any
	if json.Unmarshal([]byte(body), &obj) != nil || obj == nil {
		return body
	}

	for _, p := range inv.BodyParameters {
		obj[p.Key] = p.Value
	}

	out, err := json.Marshal(obj)
	if err != nil {
		return body
	}

	return string(out)
}

// authorizeRequest adds the connection's credentials: Basic auth, the API key
// header, or an OAuth bearer token.
func (m *Mock) authorizeRequest(ctx context.Context, req *http.Request, conn *driver.Connection) error {
	ap := &conn.AuthParameters

	switch {
	case ap.Basic != nil:
		req.SetBasicAuth(ap.Basic.Username, ap.Basic.Password)
	case ap.APIKey != nil:
		req.Header.Set(ap.APIKey.APIKeyName, ap.APIKey.APIKeyValue)
	case ap.OAuth != nil:
		token, err := m.oauthToken(ctx, conn)
		if err != nil {
			return err
		}

		req.Header.Set("Authorization", "Bearer "+token)
	}

	return nil
}

// rateLimiter spaces API destination calls so each destination gets at most its
// InvocationRateLimitPerSecond calls in any one-second window. A caller that
// finds the window full reserves a slot in the next one and waits for it, with
// no lock held while it waits.
type rateLimiter struct {
	mu      sync.Mutex
	windows map[string]*rateWindow
}

type rateWindow struct {
	start time.Time
	count int
}

// reserve claims a call slot for key and returns how long to wait before using it.
func (l *rateLimiter) reserve(key string, perSecond int, now time.Time) time.Duration {
	if perSecond <= 0 {
		return 0
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.windows == nil {
		l.windows = map[string]*rateWindow{}
	}

	w, ok := l.windows[key]
	if !ok || now.Sub(w.start) >= time.Second {
		w = &rateWindow{start: now}
		l.windows[key] = w
	}

	if w.count >= perSecond {
		w.start = w.start.Add(time.Second)
		w.count = 0
	}

	w.count++

	return max(w.start.Sub(now), 0)
}
