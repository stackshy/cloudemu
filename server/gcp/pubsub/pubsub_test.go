package pubsub_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu"
	gcpserver "github.com/stackshy/cloudemu/server/gcp"
)

const project = "demo"

func topicURL(name string) string {
	return "/v1/projects/" + project + "/topics/" + name
}

func subURL(name string) string {
	return "/v1/projects/" + project + "/subscriptions/" + name
}

func newServer(t *testing.T) *httptest.Server {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := httptest.NewServer(gcpserver.New(gcpserver.Drivers{
		PubSub:    cloud.PubSub,
		Firestore: cloud.Firestore, // exercise registration ordering
	}))
	t.Cleanup(srv.Close)

	return srv
}

func TestMatchesAcceptsTopicsAndSubscriptions(t *testing.T) {
	srv := newServer(t)

	for _, p := range []string{
		"/v1/projects/demo/topics",
		"/v1/projects/demo/subscriptions",
	} {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}

		_ = resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s = %d, want 200", p, resp.StatusCode)
		}
	}
}

func TestMatchesRejectsFirestorePath(t *testing.T) {
	srv := newServer(t)

	resp, err := http.Post(
		srv.URL+"/v1/projects/demo/databases/(default)/documents:commit",
		"application/json", strings.NewReader(`{"writes":[]}`))
	if err != nil {
		t.Fatalf("firestore POST: %v", err)
	}

	defer resp.Body.Close()

	// Firestore handler should claim it (200), not pubsub.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("firestore commit = %d, want 200 (firestore handler should win)", resp.StatusCode)
	}
}

func TestTopicLifecycle(t *testing.T) {
	srv := newServer(t)

	put := doRequest(t, srv, http.MethodPut, topicURL("orders"), `{}`)
	if put.StatusCode != http.StatusOK {
		t.Fatalf("PUT topic = %d, body: %s", put.StatusCode, readBody(t, put))
	}

	get := doRequest(t, srv, http.MethodGet, topicURL("orders"), "")

	var topicGot struct {
		Name string `json:"name"`
	}

	if err := json.NewDecoder(get.Body).Decode(&topicGot); err != nil {
		t.Fatalf("decode: %v", err)
	}

	_ = get.Body.Close()

	if !strings.HasSuffix(topicGot.Name, "/topics/orders") {
		t.Fatalf("name = %q, want suffix /topics/orders", topicGot.Name)
	}

	del := doRequest(t, srv, http.MethodDelete, topicURL("orders"), "")
	if del.StatusCode != http.StatusOK {
		t.Fatalf("DELETE topic = %d", del.StatusCode)
	}

	missing := doRequest(t, srv, http.MethodGet, topicURL("orders"), "")
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("post-delete GET = %d, want 404", missing.StatusCode)
	}
}

func TestSubscriptionLifecycle(t *testing.T) {
	srv := newServer(t)

	if r := doRequest(t, srv, http.MethodPut, topicURL("events"), `{}`); r.StatusCode != http.StatusOK {
		t.Fatalf("create topic: %d", r.StatusCode)
	}

	body := `{"topic":"projects/demo/topics/events","ackDeadlineSeconds":30}`
	put := doRequest(t, srv, http.MethodPut, subURL("events"), body)
	if put.StatusCode != http.StatusOK {
		t.Fatalf("PUT subscription = %d, body: %s", put.StatusCode, readBody(t, put))
	}

	get := doRequest(t, srv, http.MethodGet, subURL("events"), "")
	if get.StatusCode != http.StatusOK {
		t.Fatalf("GET subscription = %d", get.StatusCode)
	}
}

func TestPublishAndPull(t *testing.T) {
	srv := newServer(t)

	if r := doRequest(t, srv, http.MethodPut, topicURL("loop"), `{}`); r.StatusCode != http.StatusOK {
		t.Fatalf("create topic: %d", r.StatusCode)
	}

	pubBody := `{"messages":[{"data":"` +
		base64.StdEncoding.EncodeToString([]byte("hello")) + `"}]}`

	pub := doRequest(t, srv, http.MethodPost, topicURL("loop")+":publish", pubBody)
	if pub.StatusCode != http.StatusOK {
		t.Fatalf("publish = %d", pub.StatusCode)
	}

	pull := doRequest(t, srv, http.MethodPost, subURL("loop")+":pull", `{"maxMessages":1}`)
	if pull.StatusCode != http.StatusOK {
		t.Fatalf("pull = %d", pull.StatusCode)
	}

	var pullResp struct {
		ReceivedMessages []struct {
			AckID   string `json:"ackId"`
			Message struct {
				MessageID string `json:"messageId"`
				Data      string `json:"data"`
			} `json:"message"`
		} `json:"receivedMessages"`
	}

	if err := json.NewDecoder(pull.Body).Decode(&pullResp); err != nil {
		t.Fatalf("decode pull: %v", err)
	}

	_ = pull.Body.Close()

	if len(pullResp.ReceivedMessages) != 1 {
		t.Fatalf("got %d messages, want 1", len(pullResp.ReceivedMessages))
	}

	got, _ := base64.StdEncoding.DecodeString(pullResp.ReceivedMessages[0].Message.Data)
	if string(got) != "hello" {
		t.Fatalf("body = %q, want hello", got)
	}

	ackBody := `{"ackIds":["` + pullResp.ReceivedMessages[0].AckID + `"]}`
	ack := doRequest(t, srv, http.MethodPost, subURL("loop")+":acknowledge", ackBody)
	if ack.StatusCode != http.StatusOK {
		t.Fatalf("ack = %d", ack.StatusCode)
	}
}

func TestPullEmpty(t *testing.T) {
	srv := newServer(t)

	if r := doRequest(t, srv, http.MethodPut, topicURL("empty"), `{}`); r.StatusCode != http.StatusOK {
		t.Fatalf("create topic: %d", r.StatusCode)
	}

	resp := doRequest(t, srv, http.MethodPost, subURL("empty")+":pull", `{"maxMessages":1}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pull = %d, want 200", resp.StatusCode)
	}

	body := readBody(t, resp)
	if strings.Contains(body, `"ackId"`) {
		t.Fatalf("expected no messages, got %s", body)
	}
}

func TestPublishToMissingTopic(t *testing.T) {
	srv := newServer(t)

	pub := doRequest(t, srv, http.MethodPost, topicURL("nope")+":publish",
		`{"messages":[{"data":"eA=="}]}`)
	if pub.StatusCode != http.StatusNotFound {
		t.Fatalf("publish missing = %d, want 404", pub.StatusCode)
	}
}

func TestListTopics(t *testing.T) {
	srv := newServer(t)

	for _, n := range []string{"a", "b"} {
		_ = doRequest(t, srv, http.MethodPut, topicURL(n), `{}`)
	}

	resp := doRequest(t, srv, http.MethodGet, "/v1/projects/"+project+"/topics", "")

	var got struct {
		Topics []map[string]any `json:"topics"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	_ = resp.Body.Close()

	if len(got.Topics) != 2 {
		t.Fatalf("listed %d topics, want 2", len(got.Topics))
	}
}

// helpers --------------------------------------------------------------------

func doRequest(t *testing.T, srv *httptest.Server, method, path, body string) *http.Response {
	t.Helper()

	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}

	req, _ := http.NewRequestWithContext(context.Background(), method, srv.URL+path, rdr)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}

	return resp
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()

	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return strings.TrimSpace(string(b))
}
