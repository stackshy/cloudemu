package bedrockagentruntime_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsbedrock "github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
	awsba "github.com/aws/aws-sdk-go-v2/service/bedrockagent"
	batypes "github.com/aws/aws-sdk-go-v2/service/bedrockagent/types"
	agentruntime "github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime"
	rttypes "github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime/types"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newGuardrail creates a Bedrock guardrail that denies the word "lottery".
func (s *stack) newGuardrail(t *testing.T) string {
	t.Helper()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")))
	require.NoError(t, err)

	client := awsbedrock.NewFromConfig(cfg, func(o *awsbedrock.Options) { o.BaseEndpoint = aws.String(s.url) })

	out, err := client.CreateGuardrail(context.Background(), &awsbedrock.CreateGuardrailInput{
		Name:                    aws.String("no-lottery"),
		BlockedInputMessaging:   aws.String("Sorry, I can't talk about that."),
		BlockedOutputsMessaging: aws.String("Sorry, I can't say that."),
		WordPolicyConfig: &bedrocktypes.GuardrailWordPolicyConfig{
			WordsConfig: []bedrocktypes.GuardrailWordConfig{{Text: aws.String("lottery")}},
		},
	})
	require.NoError(t, err)

	return aws.ToString(out.GuardrailId)
}

func TestSDKGuardrailIntervenes(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	guardrailID := s.newGuardrail(t)
	agentID := s.newAgent(t, "guarded", func(in *awsba.CreateAgentInput) {
		in.GuardrailConfiguration = &batypes.GuardrailConfiguration{
			GuardrailIdentifier: aws.String(guardrailID), GuardrailVersion: aws.String("DRAFT"),
		}
	})

	blocked, err := invoke(t, s, &agentruntime.InvokeAgentInput{
		AgentId: aws.String(agentID), AgentAliasId: aws.String("TSTALIASID"), SessionId: aws.String("s-1"),
		InputText: aws.String("pick my lottery numbers"), EnableTrace: aws.Bool(true),
	})
	require.NoError(t, err)
	assert.Equal(t, "Sorry, I can't talk about that.", blocked.text)

	var guardrailTrace bool

	for _, tp := range blocked.traces {
		if g, ok := tp.Trace.(*rttypes.TraceMemberGuardrailTrace); ok {
			guardrailTrace = g.Value.Action == rttypes.GuardrailActionIntervened
		}
	}

	assert.True(t, guardrailTrace, "an INTERVENED guardrail trace is emitted")

	allowed, err := invoke(t, s, &agentruntime.InvokeAgentInput{
		AgentId: aws.String(agentID), AgentAliasId: aws.String("TSTALIASID"), SessionId: aws.String("s-2"),
		InputText: aws.String("hello there"),
	})
	require.NoError(t, err)
	assert.Contains(t, allowed.text, "hello there")

	kbID, _ := s.newKnowledgeBase(t, "faq", map[string]string{"a": "The lottery draws on Fridays."})

	res, err := s.runtime.Retrieve(ctx, &agentruntime.RetrieveInput{
		KnowledgeBaseId: aws.String(kbID), RetrievalQuery: &rttypes.KnowledgeBaseQuery{Text: aws.String("when is the lottery")},
		GuardrailConfiguration: &rttypes.GuardrailConfiguration{GuardrailId: aws.String(guardrailID), GuardrailVersion: aws.String("DRAFT")},
	})
	require.NoError(t, err)
	assert.Equal(t, rttypes.GuadrailActionIntervened, res.GuardrailAction)
	assert.Empty(t, res.RetrievalResults)
}

func TestSDKInvokeAgentPublishesMetrics(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agentID := s.newAgent(t, "metered", nil)

	_, err := invoke(t, s, &agentruntime.InvokeAgentInput{
		AgentId: aws.String(agentID), AgentAliasId: aws.String("TSTALIASID"), SessionId: aws.String("s-1"),
		InputText: aws.String("hello"),
	})
	require.NoError(t, err)

	_, err = s.runtime.InvokeAgent(ctx, &agentruntime.InvokeAgentInput{
		AgentId: aws.String("MISSING000"), AgentAliasId: aws.String("TSTALIASID"), SessionId: aws.String("s-1"),
	})
	require.Error(t, err)

	metrics, err := s.cw.ListMetrics(ctx, &cloudwatch.ListMetricsInput{Namespace: aws.String("AWS/Bedrock/Agents")})
	require.NoError(t, err)

	names := map[string]bool{}
	dimSets := map[int]bool{}

	for _, m := range metrics.Metrics {
		names[aws.ToString(m.MetricName)] = true
		dimSets[len(m.Dimensions)] = true
	}

	for _, n := range []string{"InvocationCount", "TotalTime", "InvocationClientErrors", "ModelInvocationCount",
		"ModelLatency", "InputTokenCount", "OutputTokenCount"} {
		assert.True(t, names[n], "metric %s published", n)
	}

	assert.True(t, dimSets[1] && dimSets[2] && dimSets[3], "Operation; Operation+ModelId; Operation+AgentAliasArn+ModelId")

	stats, err := s.cw.GetMetricStatistics(ctx, &cloudwatch.GetMetricStatisticsInput{
		Namespace: aws.String("AWS/Bedrock/Agents"), MetricName: aws.String("InvocationCount"),
		Dimensions: []cwtypes.Dimension{{Name: aws.String("Operation"), Value: aws.String("InvokeAgent")}},
		StartTime:  aws.Time(s.clock.Now().Add(-3600e9)), EndTime: aws.Time(s.clock.Now().Add(3600e9)),
		Period: aws.Int32(3600), Statistics: []cwtypes.Statistic{cwtypes.StatisticSum},
	})
	require.NoError(t, err)
	require.NotEmpty(t, stats.Datapoints)
	assert.InDelta(t, 1.0, aws.ToFloat64(stats.Datapoints[0].Sum), 0.001)
}
