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
