package cloudemu_test

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
	awsserver "github.com/stackshy/cloudemu/server/aws"
	azureserver "github.com/stackshy/cloudemu/server/azure"
	gcpserver "github.com/stackshy/cloudemu/server/gcp"
)

// TestMessageQueueSDKCompat_CrossProvider drives a send→receive→delete loop
// through every SDK-compat MQ handler (AWS SQS, Azure Service Bus,
// GCP Pub/Sub) using raw HTTP. It validates wire-level routing and that the
// portable message queue driver's Send/Receive/Delete chain is reachable
// from each provider's HTTP surface.
func TestMessageQueueSDKCompat_CrossProvider(t *testing.T) {
	t.Run("aws-sqs", func(t *testing.T) {
		cloud := cloudemu.NewAWS()
		ts := httptest.NewServer(awsserver.New(awsserver.Drivers{SQS: cloud.SQS}))
		t.Cleanup(ts.Close)

		// Create.
		create := postSQS(t, ts.URL, "AmazonSQS.CreateQueue", `{"QueueName":"loop"}`)
		mqMustStatus(t, create, http.StatusOK)

		var createOut struct {
			QueueURL string `json:"QueueUrl"`
		}
		mustJSON(t, create, &createOut)

		// Send.
		mqMustStatus(t, postSQS(t, ts.URL, "AmazonSQS.SendMessage",
			`{"QueueUrl":"`+createOut.QueueURL+`","MessageBody":"hello"}`), http.StatusOK)

		// Receive.
		recv := postSQS(t, ts.URL, "AmazonSQS.ReceiveMessage",
			`{"QueueUrl":"`+createOut.QueueURL+`","MaxNumberOfMessages":1}`)
		mqMustStatus(t, recv, http.StatusOK)

		var recvOut struct {
			Messages []struct {
				Body          string `json:"Body"`
				ReceiptHandle string `json:"ReceiptHandle"`
			} `json:"Messages"`
		}
		mustJSON(t, recv, &recvOut)

		if len(recvOut.Messages) != 1 || recvOut.Messages[0].Body != "hello" {
			t.Fatalf("Receive: got %+v", recvOut.Messages)
		}

		// Delete message.
		mqMustStatus(t, postSQS(t, ts.URL, "AmazonSQS.DeleteMessage",
			`{"QueueUrl":"`+createOut.QueueURL+`","ReceiptHandle":"`+recvOut.Messages[0].ReceiptHandle+`"}`),
			http.StatusOK)

		// Delete queue.
		mqMustStatus(t, postSQS(t, ts.URL, "AmazonSQS.DeleteQueue",
			`{"QueueUrl":"`+createOut.QueueURL+`"}`), http.StatusOK)
	})

	t.Run("azure-servicebus", func(t *testing.T) {
		cloud := cloudemu.NewAzure()
		ts := httptest.NewServer(azureserver.New(azureserver.Drivers{ServiceBus: cloud.ServiceBus}))
		t.Cleanup(ts.Close)

		const (
			subID  = "00000000-0000-0000-0000-000000000000"
			rgName = "rg-1"
			nsName = "ns-test"
			apiVer = "?api-version=2022-10-01-preview"
		)

		queueARM := ts.URL + "/subscriptions/" + subID +
			"/resourceGroups/" + rgName +
			"/providers/Microsoft.ServiceBus/namespaces/" + nsName +
			"/queues/loop" + apiVer

		// Create queue (control plane).
		mqMustStatus(t, putRequest(t, queueARM, `{"properties":{}}`), http.StatusOK)

		// Send (data plane).
		mqMustStatus(t, postRaw(t, ts.URL+"/"+nsName+"/loop/messages", "hello"), http.StatusCreated)

		// Receive (data plane).
		recv := deleteRequest(t, ts.URL+"/"+nsName+"/loop/messages/head")
		mqMustStatus(t, recv, http.StatusOK)

		body := mqReadBody(t, recv)
		if body != "hello" {
			t.Fatalf("body = %q, want hello", body)
		}

		// Delete queue.
		mqMustStatus(t, deleteRequest(t, queueARM), http.StatusOK)
	})

	t.Run("gcp-pubsub", func(t *testing.T) {
		cloud := cloudemu.NewGCP()
		ts := httptest.NewServer(gcpserver.New(gcpserver.Drivers{PubSub: cloud.PubSub}))
		t.Cleanup(ts.Close)

		const project = "demo"
		topicURL := ts.URL + "/v1/projects/" + project + "/topics/loop"
		subURL := ts.URL + "/v1/projects/" + project + "/subscriptions/loop"

		// Create topic.
		mqMustStatus(t, putRequest(t, topicURL, `{}`), http.StatusOK)

		// Create subscription.
		mqMustStatus(t, putRequest(t, subURL,
			`{"topic":"projects/demo/topics/loop"}`), http.StatusOK)

		// Publish.
		pubBody := `{"messages":[{"data":"` + base64.StdEncoding.EncodeToString([]byte("hello")) + `"}]}`
		mqMustStatus(t, postRaw(t, topicURL+":publish", pubBody), http.StatusOK)

		// Pull.
		pull := postRaw(t, subURL+":pull", `{"maxMessages":1}`)
		mqMustStatus(t, pull, http.StatusOK)

		var pullResp struct {
			ReceivedMessages []struct {
				AckID   string `json:"ackId"`
				Message struct {
					Data string `json:"data"`
				} `json:"message"`
			} `json:"receivedMessages"`
		}
		mustJSON(t, pull, &pullResp)

		if len(pullResp.ReceivedMessages) != 1 {
			t.Fatalf("Pull: got %d messages, want 1", len(pullResp.ReceivedMessages))
		}

		decoded, _ := base64.StdEncoding.DecodeString(pullResp.ReceivedMessages[0].Message.Data)
		if string(decoded) != "hello" {
			t.Fatalf("payload = %q, want hello", decoded)
		}

		// Ack.
		ackBody := `{"ackIds":["` + pullResp.ReceivedMessages[0].AckID + `"]}`
		mqMustStatus(t, postRaw(t, subURL+":acknowledge", ackBody), http.StatusOK)

		// Delete topic.
		mqMustStatus(t, deleteRequest(t, topicURL), http.StatusOK)
	})
}

// helpers --------------------------------------------------------------------

func postSQS(t *testing.T, baseURL, target, body string) *http.Response {
	t.Helper()

	req, _ := http.NewRequestWithContext(context.Background(),
		http.MethodPost, baseURL+"/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", target)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", target, err)
	}

	return resp
}

func postRaw(t *testing.T, url, body string) *http.Response {
	t.Helper()

	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}

	return resp
}

func putRequest(t *testing.T, url, body string) *http.Response {
	t.Helper()

	req, _ := http.NewRequestWithContext(context.Background(),
		http.MethodPut, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", url, err)
	}

	return resp
}

func deleteRequest(t *testing.T, url string) *http.Response {
	t.Helper()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodDelete, url, nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE %s: %v", url, err)
	}

	return resp
}

func mqMustStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()

	if resp.StatusCode != want {
		body := mqReadBody(t, resp)
		t.Fatalf("status = %d, want %d; body: %s", resp.StatusCode, want, body)
	}
}

func mustJSON(t *testing.T, resp *http.Response, v any) {
	t.Helper()

	defer resp.Body.Close()

	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
}

func mqReadBody(t *testing.T, resp *http.Response) string {
	t.Helper()

	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return strings.TrimSpace(string(b))
}
