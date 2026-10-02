package serverkit

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// metricPutter is the PutMetricData surface of the Azure and GCP monitor mocks.
type metricPutter interface {
	PutMetricData(ctx context.Context, data []mondriver.MetricDatum) error
}

// tickServe is a running single-provider serve App on a fake clock, with an
// HTTP front for its wire handler and a webhook receiver counting hits.
type tickServe struct {
	app     *App
	clock   *config.FakeClock
	api     *httptest.Server
	hook    *httptest.Server
	hits    *atomic.Int64
	metrics metricPutter
}

// startTickServe serves one provider with a short tick on a fake clock, so a
// test can move time forward without waiting for it.
func startTickServe(t *testing.T, provider string, tick time.Duration) *tickServe {
	t.Helper()

	clock := config.NewFakeClock(time.Now())

	app := newTestApp(t, Config{
		Providers:    []string{provider},
		Host:         "127.0.0.1",
		Ports:        map[string]string{provider: "0"},
		TickInterval: tick,
		BaseOptions:  []config.Option{config.WithClock(clock)},
		Out:          io.Discard,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- app.Serve(ctx) }()

	t.Cleanup(func() {
		cancel()

		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})

	api := httptest.NewServer(app.handlerFor(app.backends[provider], app.seedFor(provider)))
	t.Cleanup(api.Close)

	hits := &atomic.Int64{}
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(hook.Close)

	if len(app.tickables) != 1 {
		t.Fatalf("%s tickables = %d, want 1", provider, len(app.tickables))
	}

	metrics, ok := app.tickables[0].(metricPutter)
	if !ok {
		t.Fatalf("%s tickable %T does not take metric data", provider, app.tickables[0])
	}

	return &tickServe{app: app, clock: clock, api: api, hook: hook, hits: hits, metrics: metrics}
}

// call sends one JSON request and returns the response body.
func (s *tickServe) call(t *testing.T, method, path, body string) string {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), method, s.api.URL+path, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= http.StatusMultipleChoices {
		t.Fatalf("%s %s = %d: %s", method, path, resp.StatusCode, out)
	}

	return string(out)
}

// putFutureDatum stores a breaching datum dated ahead of the fake clock, so
// the alert created next cannot see it yet.
func (s *tickServe) putFutureDatum(t *testing.T, d mondriver.MetricDatum) {
	t.Helper()

	d.Timestamp = s.clock.Now().Add(30 * time.Second)

	if err := s.metrics.PutMetricData(context.Background(), []mondriver.MetricDatum{d}); err != nil {
		t.Fatalf("PutMetricData: %v", err)
	}
}

// expectHookAfterAdvance moves the fake clock past the next evaluation and
// waits for the webhook. No monitor read is made, so only a tick can fire it.
func (s *tickServe) expectHookAfterAdvance(t *testing.T) {
	t.Helper()

	time.Sleep(100 * time.Millisecond) // let a few ticks run before time moves

	if got := s.hits.Load(); got != 0 {
		t.Fatalf("webhook hit %d times before the datum was due", got)
	}

	s.clock.Advance(61 * time.Second)

	deadline := time.Now().Add(5 * time.Second)
	for s.hits.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("webhook never received the alert notification")
		}

		time.Sleep(10 * time.Millisecond)
	}
}

// TestServeTickFiresAzureActionGroup checks serve evaluates a due Azure
// metric alert on its own and delivers to the action group's webhook.
func TestServeTickFiresAzureActionGroup(t *testing.T) {
	s := startTickServe(t, providerAzure, 20*time.Millisecond)

	const (
		rg      = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg-tick"
		vm      = rg + "/providers/Microsoft.Compute/virtualMachines/vm1"
		agID    = rg + "/providers/microsoft.insights/actionGroups/ag-tick"
		version = "?api-version=2021-04-01"
	)

	s.call(t, http.MethodPut, rg+version, `{"location":"eastus"}`)
	s.call(t, http.MethodPut, agID+"?api-version=2023-01-01",
		`{"location":"global","properties":{"groupShortName":"tick","enabled":true,`+
			`"webhookReceivers":[{"name":"hook","serviceUri":"`+s.hook.URL+`"}]}}`)

	s.putFutureDatum(t, mondriver.MetricDatum{
		Namespace: "Microsoft.Compute/virtualMachines", MetricName: "Percentage CPU", Value: 95,
		Dimensions: map[string]string{"resourceId": vm},
	})

	alert := `{"location":"global","properties":{"severity":3,"enabled":true,"scopes":["` + vm + `"],` +
		`"evaluationFrequency":"PT1M","windowSize":"PT1M","actions":[{"actionGroupId":"` + agID + `"}],` +
		`"criteria":{"odata.type":"Microsoft.Azure.Monitor.SingleResourceMultipleMetricCriteria","allOf":[{` +
		`"criterionType":"StaticThresholdCriterion","name":"cpu","metricName":"Percentage CPU",` +
		`"metricNamespace":"Microsoft.Compute/virtualMachines","operator":"GreaterThan","threshold":50,` +
		`"timeAggregation":"Average"}]}}}`
	s.call(t, http.MethodPut, rg+"/providers/Microsoft.Insights/metricAlerts/cpu-tick?api-version=2018-03-01", alert)

	s.expectHookAfterAdvance(t)
}

// TestServeTickFiresGCPNotificationChannel checks serve evaluates a due GCP
// alert policy on its own and delivers to its webhook channel.
func TestServeTickFiresGCPNotificationChannel(t *testing.T) {
	s := startTickServe(t, providerGCP, 20*time.Millisecond)

	ch := s.call(t, http.MethodPost, "/v3/projects/p1/notificationChannels",
		`{"type":"webhook_tokenauth","displayName":"hook","labels":{"url":"`+s.hook.URL+`"}}`)

	name := between(ch, `"name":"`, `"`)
	if name == "" {
		t.Fatalf("channel create returned no name: %s", ch)
	}

	s.putFutureDatum(t, mondriver.MetricDatum{Namespace: "gcp", MetricName: "metric", Value: 1})

	s.call(t, http.MethodPost, "/v3/projects/p1/alertPolicies",
		`{"displayName":"tick","notificationChannels":["`+name+`"]}`)

	s.expectHookAfterAdvance(t)
}

// between returns the text after start up to the next end, or "".
func between(s, start, end string) string {
	_, rest, ok := strings.Cut(s, start)
	if !ok {
		return ""
	}

	v, _, _ := strings.Cut(rest, end)

	return v
}
