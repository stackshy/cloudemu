package bedrock

import (
	"context"
	"sync"
	"testing"

	bedrockdriver "github.com/stackshy/cloudemu/v2/services/bedrock/driver"
)

// TestConcurrentGuardrailAccessRace hammers the guardrail record (versions slice
// + draft) with concurrent mutators and readers. Run under `go test -race` it
// guards against the data race fixed by the guardrailRecord mutex (M4) and the
// policy deep-copy (M3).
func TestConcurrentGuardrailAccessRace(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateGuardrail(ctx, bedrockdriver.GuardrailConfig{
		Name:                    "race-guard",
		BlockedInputMessaging:   "x",
		BlockedOutputsMessaging: "y",
		GuardrailPolicies: bedrockdriver.GuardrailPolicies{
			ContentPolicy: &bedrockdriver.GuardrailContentPolicy{
				Filters: []bedrockdriver.GuardrailContentFilter{{Type: "VIOLENCE", InputStrength: "HIGH", OutputStrength: "HIGH"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateGuardrail: %v", err)
	}

	const workers = 8

	const iters = 50

	var wg sync.WaitGroup

	for w := 0; w < workers; w++ {
		wg.Add(1)

		go func(id int) {
			defer wg.Done()

			for i := 0; i < iters; i++ {
				switch id % 4 {
				case 0:
					_, _, _ = m.CreateGuardrailVersion(ctx, "race-guard", "v")
				case 1:
					_, _ = m.UpdateGuardrail(ctx, "race-guard", bedrockdriver.GuardrailConfig{
						Name: "race-guard", BlockedInputMessaging: "x2", BlockedOutputsMessaging: "y2",
					})
				case 2:
					_, _ = m.ListGuardrails(ctx, "race-guard")
				default:
					_, _ = m.GetGuardrail(ctx, "race-guard", "")
				}
			}
		}(w)
	}

	wg.Wait()
}

// TestConcurrentEvalJobAccessRace hammers an evaluation job with concurrent Stop
// (mutating status through the stored pointer) and Get/List reads, catching the
// shared-pointer mutation class under `go test -race`.
func TestConcurrentEvalJobAccessRace(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateEvaluationJob(ctx, bedrockdriver.EvaluationJobConfig{
		JobName:          "race-eval",
		RoleARN:          "arn:aws:iam::123456789012:role/bedrock",
		EvaluationConfig: []byte(`{"automated":{}}`),
		InferenceConfig:  []byte(`{"models":[]}`),
		OutputDataS3URI:  "s3://bucket/eval/",
	})
	if err != nil {
		t.Fatalf("CreateEvaluationJob: %v", err)
	}

	const workers = 8

	const iters = 50

	var wg sync.WaitGroup

	for w := 0; w < workers; w++ {
		wg.Add(1)

		go func(id int) {
			defer wg.Done()

			for i := 0; i < iters; i++ {
				switch id % 3 {
				case 0:
					_ = m.StopEvaluationJob(ctx, "race-eval")
				case 1:
					_, _ = m.GetEvaluationJob(ctx, "race-eval")
				default:
					_, _ = m.ListEvaluationJobs(ctx)
				}
			}
		}(w)
	}

	wg.Wait()
}

// TestConcurrentMarketplaceEndpointAccessRace hammers the marketplace endpoint
// copy-on-write mutators (Register/Update) concurrently with Get/List reads,
// exercising the copy-on-write paths under `go test -race`.
func TestConcurrentMarketplaceEndpointAccessRace(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	created, err := m.CreateMarketplaceModelEndpoint(ctx, bedrockdriver.MarketplaceEndpointConfig{
		EndpointName:          "race-endpoint",
		ModelSourceIdentifier: "arn:aws:sagemaker:us-east-1:aws:hub-content/model/1",
		EndpointConfig:        []byte(`{"sageMaker":{"instanceType":"ml.m5.large"}}`),
	})
	if err != nil {
		t.Fatalf("CreateMarketplaceModelEndpoint: %v", err)
	}

	arn := created.EndpointARN

	const workers = 8

	const iters = 50

	var wg sync.WaitGroup

	for w := 0; w < workers; w++ {
		wg.Add(1)

		go func(id int) {
			defer wg.Done()

			for i := 0; i < iters; i++ {
				switch id % 4 {
				case 0:
					_, _ = m.RegisterMarketplaceModelEndpoint(ctx, arn, "arn:model/source-race")
				case 1:
					_, _ = m.UpdateMarketplaceModelEndpoint(ctx, arn, []byte(`{"sageMaker":{"instanceType":"ml.m5.xlarge"}}`))
				case 2:
					_, _ = m.GetMarketplaceModelEndpoint(ctx, arn)
				default:
					_, _ = m.ListMarketplaceModelEndpoints(ctx)
				}
			}
		}(w)
	}

	wg.Wait()
}

// TestConcurrentARPolicyAccessRace hammers the automated-reasoning-policy
// copy-on-write Update mutator concurrently with Get/List reads, exercising the
// copy-on-write path under `go test -race`.
func TestConcurrentARPolicyAccessRace(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	policy, err := m.CreateAutomatedReasoningPolicy(ctx, bedrockdriver.AutomatedReasoningPolicyConfig{
		Name:             "race-policy",
		PolicyDefinition: []byte(`{"rules":[]}`),
	})
	if err != nil {
		t.Fatalf("CreateAutomatedReasoningPolicy: %v", err)
	}

	arn := policy.ARN

	const workers = 8

	const iters = 50

	var wg sync.WaitGroup

	for w := 0; w < workers; w++ {
		wg.Add(1)

		go func(id int) {
			defer wg.Done()

			for i := 0; i < iters; i++ {
				switch id % 3 {
				case 0:
					_, _ = m.UpdateAutomatedReasoningPolicy(ctx, arn, bedrockdriver.AutomatedReasoningPolicyUpdate{
						Description:      "updated",
						PolicyDefinition: []byte(`{"rules":[{"id":"r1"}]}`),
					})
				case 1:
					_, _ = m.GetAutomatedReasoningPolicy(ctx, arn)
				default:
					_, _ = m.ListAutomatedReasoningPolicies(ctx)
				}
			}
		}(w)
	}

	wg.Wait()
}
