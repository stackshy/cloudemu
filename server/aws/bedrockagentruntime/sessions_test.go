package bedrockagentruntime_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	agentruntime "github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime"
	rttypes "github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSDKSessionLifecycle(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	created, err := s.runtime.CreateSession(ctx, &agentruntime.CreateSessionInput{
		SessionMetadata: map[string]string{"user": "alice"}, Tags: map[string]string{"team": "ml"},
	})
	require.NoError(t, err)
	assert.Equal(t, rttypes.SessionStatusActive, created.SessionStatus)
	assert.Regexp(t, `^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`, aws.ToString(created.SessionId))

	id, arn := created.SessionId, created.SessionArn
	assert.Equal(t, "arn:aws:bedrock:us-east-1:123456789012:session/"+aws.ToString(id), aws.ToString(arn))

	got, err := s.runtime.GetSession(ctx, &agentruntime.GetSessionInput{SessionIdentifier: arn})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"user": "alice"}, got.SessionMetadata)

	upd, err := s.runtime.UpdateSession(ctx, &agentruntime.UpdateSessionInput{
		SessionIdentifier: id, SessionMetadata: map[string]string{"user": "bob"},
	})
	require.NoError(t, err)
	assert.Equal(t, rttypes.SessionStatusActive, upd.SessionStatus)

	tags, err := s.runtime.ListTagsForResource(ctx, &agentruntime.ListTagsForResourceInput{ResourceArn: arn})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"team": "ml"}, tags.Tags)

	_, err = s.runtime.TagResource(ctx, &agentruntime.TagResourceInput{ResourceArn: arn, Tags: map[string]string{"env": "dev"}})
	require.NoError(t, err)

	_, err = s.runtime.UntagResource(ctx, &agentruntime.UntagResourceInput{ResourceArn: arn, TagKeys: []string{"team"}})
	require.NoError(t, err)

	tags, err = s.runtime.ListTagsForResource(ctx, &agentruntime.ListTagsForResourceInput{ResourceArn: arn})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"env": "dev"}, tags.Tags)

	_, err = s.runtime.CreateSession(ctx, &agentruntime.CreateSessionInput{})
	require.NoError(t, err)

	page, err := s.runtime.ListSessions(ctx, &agentruntime.ListSessionsInput{MaxResults: aws.Int32(1)})
	require.NoError(t, err)
	require.Len(t, page.SessionSummaries, 1)
	require.NotNil(t, page.NextToken)

	_, err = s.runtime.DeleteSession(ctx, &agentruntime.DeleteSessionInput{SessionIdentifier: id})
	requireAPIError(t, err, "ConflictException", http.StatusConflict)

	ended, err := s.runtime.EndSession(ctx, &agentruntime.EndSessionInput{SessionIdentifier: id})
	require.NoError(t, err)
	assert.Equal(t, rttypes.SessionStatusEnded, ended.SessionStatus)

	_, err = s.runtime.UpdateSession(ctx, &agentruntime.UpdateSessionInput{SessionIdentifier: id})
	requireAPIError(t, err, "ConflictException", http.StatusConflict)

	_, err = s.runtime.CreateInvocation(ctx, &agentruntime.CreateInvocationInput{SessionIdentifier: id})
	requireAPIError(t, err, "ConflictException", http.StatusConflict)

	_, err = s.runtime.DeleteSession(ctx, &agentruntime.DeleteSessionInput{SessionIdentifier: id})
	require.NoError(t, err)

	_, err = s.runtime.GetSession(ctx, &agentruntime.GetSessionInput{SessionIdentifier: id})
	requireAPIError(t, err, "ResourceNotFoundException", http.StatusNotFound)

	_, err = s.runtime.ListTagsForResource(ctx, &agentruntime.ListTagsForResourceInput{ResourceArn: arn})
	requireAPIError(t, err, "ResourceNotFoundException", http.StatusNotFound)

	_, err = s.runtime.GetSession(ctx, &agentruntime.GetSessionInput{SessionIdentifier: aws.String("not-a-uuid")})
	requireAPIError(t, err, "ValidationException", http.StatusBadRequest)
}

func TestSDKSessionExpiresWhenIdle(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	created, err := s.runtime.CreateSession(ctx, &agentruntime.CreateSessionInput{})
	require.NoError(t, err)

	s.clock.Advance(61 * time.Minute)

	got, err := s.runtime.GetSession(ctx, &agentruntime.GetSessionInput{SessionIdentifier: created.SessionId})
	require.NoError(t, err)
	assert.Equal(t, rttypes.SessionStatusExpired, got.SessionStatus, "the idle timeout is one hour")

	_, err = s.runtime.DeleteSession(ctx, &agentruntime.DeleteSessionInput{SessionIdentifier: created.SessionId})
	require.NoError(t, err, "an expired session can be deleted")
}

func TestSDKInvocationSteps(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	sess, err := s.runtime.CreateSession(ctx, &agentruntime.CreateSessionInput{})
	require.NoError(t, err)

	inv, err := s.runtime.CreateInvocation(ctx, &agentruntime.CreateInvocationInput{
		SessionIdentifier: sess.SessionId, Description: aws.String("plan a trip"),
	})
	require.NoError(t, err)

	invID := inv.InvocationId

	_, err = s.runtime.CreateInvocation(ctx, &agentruntime.CreateInvocationInput{
		SessionIdentifier: sess.SessionId, InvocationId: invID,
	})
	requireAPIError(t, err, "ConflictException", http.StatusConflict)

	step, err := s.runtime.PutInvocationStep(ctx, &agentruntime.PutInvocationStepInput{
		SessionIdentifier: sess.SessionId, InvocationIdentifier: invID, InvocationStepTime: aws.Time(time.Now()),
		Payload: &rttypes.InvocationStepPayloadMemberContentBlocks{Value: []rttypes.BedrockSessionContentBlock{
			&rttypes.BedrockSessionContentBlockMemberText{Value: "Day 1: Paris"},
			&rttypes.BedrockSessionContentBlockMemberImage{Value: rttypes.ImageBlock{
				Format: rttypes.ImageFormatPng, Source: &rttypes.ImageSourceMemberBytes{Value: []byte{1, 2, 3}},
			}},
		}},
	})
	require.NoError(t, err)

	got, err := s.runtime.GetInvocationStep(ctx, &agentruntime.GetInvocationStepInput{
		SessionIdentifier: sess.SessionId, InvocationIdentifier: invID, InvocationStepId: step.InvocationStepId,
	})
	require.NoError(t, err)

	blocks, ok := got.InvocationStep.Payload.(*rttypes.InvocationStepPayloadMemberContentBlocks)
	require.True(t, ok)
	require.Len(t, blocks.Value, 2)
	assert.Equal(t, "Day 1: Paris", blocks.Value[0].(*rttypes.BedrockSessionContentBlockMemberText).Value)

	steps, err := s.runtime.ListInvocationSteps(ctx, &agentruntime.ListInvocationStepsInput{
		SessionIdentifier: sess.SessionId, InvocationIdentifier: invID,
	})
	require.NoError(t, err)
	assert.Len(t, steps.InvocationStepSummaries, 1)

	invs, err := s.runtime.ListInvocations(ctx, &agentruntime.ListInvocationsInput{SessionIdentifier: sess.SessionId})
	require.NoError(t, err)
	assert.Len(t, invs.InvocationSummaries, 1)

	_, err = s.runtime.PutInvocationStep(ctx, &agentruntime.PutInvocationStepInput{
		SessionIdentifier: sess.SessionId, InvocationIdentifier: aws.String("11111111-2222-4333-8444-555555555555"),
		InvocationStepTime: aws.Time(time.Now()),
		Payload: &rttypes.InvocationStepPayloadMemberContentBlocks{Value: []rttypes.BedrockSessionContentBlock{
			&rttypes.BedrockSessionContentBlockMemberText{Value: "x"},
		}},
	})
	requireAPIError(t, err, "ResourceNotFoundException", http.StatusNotFound)
}

// TestRawRequestErrorsOnEveryRoute sends each runtime route what only a hand
// written request carries: a body that is not JSON, unknown resources and bad
// paging. Every answer is a typed 4xx exception.
func TestRawRequestErrorsOnEveryRoute(t *testing.T) {
	s := newStack(t)
	const (
		agent   = "/agents/ABCDEFGHIJ/agentAliases/TSTALIASID"
		session = "/sessions/00000000-0000-4000-8000-000000000000"
		flow    = "/flows/ABCDEFGHIJ/aliases/TSTALIASID"
		kb      = "/knowledgebases/ABCDEFGHIJ"
	)

	broken := []struct{ method, path string }{
		{http.MethodPost, agent + "/sessions/s-1/text"},
		{http.MethodPost, "/agents/inline-1"},
		{http.MethodPost, kb + "/retrieve"},
		{http.MethodPost, "/retrieveAndGenerate"},
		{http.MethodPost, "/retrieveAndGenerateStream"},
		{http.MethodPost, "/rerank"},
		{http.MethodPost, "/generateQuery"},
		{http.MethodPost, "/optimize-prompt"},
		{http.MethodPost, "/agenticRetrieveStream"},
		{http.MethodPut, "/sessions/"},
		{http.MethodPut, session},
		{http.MethodPut, session + "/invocations/"},
		{http.MethodPut, session + "/invocationSteps/"},
		{http.MethodPost, session + "/invocationSteps/"},
		{http.MethodPost, session + "/invocationSteps/00000000-0000-4000-8000-000000000000"},
		{http.MethodPost, flow},
		{http.MethodPost, flow + "/executions"},
		{http.MethodPost, kb + "/datasources/ABCDEFGHIJ/documents/doc/content"},
		{http.MethodPost, "/tags/arn:aws:bedrock:us-east-1:123456789012:session/00000000-0000-4000-8000-000000000000"},
	}

	for _, c := range broken {
		status, errType := s.raw(t, c.method, c.path, "{")
		assert.Equal(t, http.StatusBadRequest, status, "%s %s with a broken body", c.method, c.path)
		assert.Equal(t, "ValidationException", errType, "%s %s", c.method, c.path)
	}

	unknown := []struct{ method, path, body string }{
		{http.MethodPatch, session, ""},
		{http.MethodPost, session + "/invocationSteps/00000000-0000-4000-8000-000000000000",
			`{"invocationIdentifier":"00000000-0000-4000-8000-000000000000"}`},
		{http.MethodPost, session + "/invocations/", ""},
		{http.MethodPost, session + "/invocationSteps/", ""},
		{http.MethodGet, agent + "/memories?memoryType=SESSION_SUMMARY&memoryId=m1", ""},
		{http.MethodDelete, agent + "/memories", ""},
		{http.MethodGet, flow + "/executions/e1", ""},
		{http.MethodGet, flow + "/executions/e1/events?eventType=Flow", ""},
		{http.MethodGet, flow + "/executions/e1/flowsnapshot", ""},
		{http.MethodGet, "/flows/ABCDEFGHIJ/executions", ""},
		{http.MethodGet, kb + "/datasources/ABCDEFGHIJ/documents/doc/content?X-Cloudemu-Content=nope", ""},
		{http.MethodPost, "/sessions/?maxResults=0", ""},
		{http.MethodPost, "/sessions/?maxResults=abc", ""},
		{http.MethodPost, session + "/invocations/?maxResults=-1", ""},
	}

	for _, c := range unknown {
		status, errType := s.raw(t, c.method, c.path, c.body)
		assert.True(t, status >= 400 && status < 500, "%s %s: %d %s", c.method, c.path, status, errType)
		assert.NotEmpty(t, errType, "%s %s: a typed exception", c.method, c.path)
	}
}
