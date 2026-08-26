package pubsub_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// TestMonitoringBreachPublishesToPubSubChannel is the end-to-end proof that an
// alert-policy breach delivers its incident to a pubsub notification channel's
// topic through the server-wired adapter: a pull subscription on that topic
// receives the incident message.
func TestMonitoringBreachPublishesToPubSubChannel(t *testing.T) {
	cloud := cloudemu.NewGCP()
	srv := httptest.NewServer(gcpserver.New(gcpserver.Drivers{
		PubSub:     cloud.PubSub,
		Monitoring: cloud.CloudMonitoring,
	}))
	t.Cleanup(srv.Close)

	// Topic + pull subscription (created before publish so it is not backlog).
	resp := doRequest(t, srv, http.MethodPut, topicURL("alerts"), `{}`)
	resp.Body.Close()
	subBody := `{"topic":"projects/` + project + `/topics/alerts"}`
	doRequest(t, srv, http.MethodPut, subURL("alerts-sub"), subBody).Body.Close()

	// Pub/Sub notification channel + alert policy referencing it.
	chResp := doRequest(t, srv, http.MethodPost,
		"/v3/projects/"+project+"/notificationChannels",
		`{"type":"pubsub","displayName":"c","labels":{"topic":"projects/`+project+`/topics/alerts"}}`)

	var ch struct {
		Name string `json:"name"`
	}
	decodeResp(t, chResp, &ch)

	if ch.Name == "" {
		t.Fatal("notification channel create returned no name")
	}

	doRequest(t, srv, http.MethodPost,
		"/v3/projects/"+project+"/alertPolicies",
		`{"displayName":"p","notificationChannels":["`+ch.Name+`"]}`).Body.Close()

	// Breach the (server-created) driver alarm: a "gcp"/"metric" datum above 0.
	if err := cloud.CloudMonitoring.PutMetricData(context.Background(), []mondriver.MetricDatum{{
		Namespace: "gcp", MetricName: "metric", Value: 1, Timestamp: time.Now(),
	}}); err != nil {
		t.Fatalf("PutMetricData: %v", err)
	}

	// The incident must be pullable off the topic's subscription.
	pull := doRequest(t, srv, http.MethodPost, subURL("alerts-sub")+":pull", `{"maxMessages":10}`)

	var out struct {
		ReceivedMessages []struct {
			Message struct {
				Data string `json:"data"`
			} `json:"message"`
		} `json:"receivedMessages"`
	}
	decodeResp(t, pull, &out)

	if len(out.ReceivedMessages) != 1 {
		t.Fatalf("want 1 pulled incident, got %d", len(out.ReceivedMessages))
	}

	data, err := base64.StdEncoding.DecodeString(out.ReceivedMessages[0].Message.Data)
	if err != nil {
		t.Fatalf("decode message data: %v", err)
	}

	if !strings.Contains(string(data), `"state":"open"`) || !strings.Contains(string(data), `"policy_name"`) {
		t.Fatalf("pulled message is not the incident: %s", data)
	}
}

func decodeResp(t *testing.T, resp *http.Response, v any) {
	t.Helper()

	defer resp.Body.Close()

	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}
