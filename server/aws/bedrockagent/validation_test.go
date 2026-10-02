package bedrockagent_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsba "github.com/aws/aws-sdk-go-v2/service/bedrockagent"
	batypes "github.com/aws/aws-sdk-go-v2/service/bedrockagent/types"

	"github.com/stackshy/cloudemu/v2/config"
	providerba "github.com/stackshy/cloudemu/v2/providers/aws/bedrockagent"
	serverba "github.com/stackshy/cloudemu/v2/server/aws/bedrockagent"
)

// rawCall sends a JSON request straight to the handler, skipping the SDK's
// client-side required-field checks so the server-side validation is what
// answers. It returns the status, the X-Amzn-Errortype header, and the message.
func rawCall(t *testing.T, h http.Handler, method, path, body string) (status int, errType, msg string) {
	t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var out struct {
		Message string `json:"message"`
	}

	_ = json.Unmarshal(rec.Body.Bytes(), &out)

	return rec.Code, rec.Header().Get("X-Amzn-Errortype"), out.Message
}

func TestRequiredFieldValidation(t *testing.T) {
	h := serverba.New(providerba.New(config.NewOptions()))

	cases := []struct {
		name, method, path, body, wantMsg string
	}{
		{
			"create agent without name", http.MethodPut, "/agents/", `{}`,
			"1 validation error detected: Value null at 'agentName' failed to satisfy constraint: Member must not be null",
		},
		{
			"create knowledge base without role and config", http.MethodPut, "/knowledgebases/", `{"name":"kb"}`,
			"2 validation errors detected: Value null at 'roleArn' failed to satisfy constraint: Member must not be null; " +
				"Value null at 'knowledgeBaseConfiguration' failed to satisfy constraint: Member must not be null",
		},
		{
			"create flow without execution role", http.MethodPost, "/flows/", `{"name":"f"}`,
			"1 validation error detected: Value null at 'executionRoleArn' failed to satisfy constraint: Member must not be null",
		},
		{
			"create prompt without name", http.MethodPost, "/prompts/", `{}`,
			"1 validation error detected: Value null at 'name' failed to satisfy constraint: Member must not be null",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, errType, msg := rawCall(t, h, tc.method, tc.path, tc.body)
			if status != http.StatusBadRequest || errType != "ValidationException" {
				t.Fatalf("got %d %q, want 400 ValidationException (msg %q)", status, errType, msg)
			}

			if msg != tc.wantMsg {
				t.Fatalf("message = %q\nwant      %q", msg, tc.wantMsg)
			}
		})
	}
}

// TestUpdateAgentRequiresFullShape covers UpdateAgent, which (unlike
// CreateAgent) requires the name, foundation model and service role.
func TestUpdateAgentRequiresFullShape(t *testing.T) {
	h := serverba.New(providerba.New(config.NewOptions()))

	req := httptest.NewRequest(http.MethodPut, "/agents/", strings.NewReader(`{"agentName":"a"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var created struct {
		Agent struct {
			AgentID string `json:"agentId"`
		} `json:"agent"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.Agent.AgentID == "" {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}

	status, errType, msg := rawCall(t, h, http.MethodPut, "/agents/"+created.Agent.AgentID+"/", `{"agentName":"a"}`)
	want := "2 validation errors detected: Value null at 'foundationModel' failed to satisfy constraint: " +
		"Member must not be null; Value null at 'agentResourceRoleArn' failed to satisfy constraint: Member must not be null"

	if status != http.StatusBadRequest || errType != "ValidationException" || msg != want {
		t.Fatalf("got %d %q %q", status, errType, msg)
	}
}

// TestVectorKnowledgeBaseRequiresStorage covers a VECTOR knowledge base
// created without its vector store.
func TestVectorKnowledgeBaseRequiresStorage(t *testing.T) {
	client := newClient(t)

	in := vectorKBInput("no-store", nil)
	in.StorageConfiguration = nil

	_, err := client.CreateKnowledgeBase(context.Background(), in)

	var ve *batypes.ValidationException
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationException, got %T: %v", err, err)
	}
}

// TestListBoundsValidation covers maxResults outside 1..1000 and a nextToken
// the service never issued.
func TestListBoundsValidation(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	_, err := client.ListAgents(ctx, &awsba.ListAgentsInput{MaxResults: aws.Int32(1001)})

	var ve *batypes.ValidationException
	if !errors.As(err, &ve) {
		t.Fatalf("maxResults 1001: expected ValidationException, got %T: %v", err, err)
	}

	want := "1 validation error detected: Value '1001' at 'maxResults' failed to satisfy constraint: " +
		"Member must have value less than or equal to 1000"
	if ve.ErrorMessage() != want {
		t.Fatalf("message = %q, want %q", ve.ErrorMessage(), want)
	}

	_, err = client.ListFlows(ctx, &awsba.ListFlowsInput{MaxResults: aws.Int32(0)})
	if !errors.As(err, &ve) {
		t.Fatalf("maxResults 0: expected ValidationException, got %T: %v", err, err)
	}

	_, err = client.ListPrompts(ctx, &awsba.ListPromptsInput{NextToken: aws.String("not-a-token")})
	if !errors.As(err, &ve) {
		t.Fatalf("bad nextToken: expected ValidationException, got %T: %v", err, err)
	}
}
