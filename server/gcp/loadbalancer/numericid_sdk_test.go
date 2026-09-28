package loadbalancer_test

import (
	"context"
	"fmt"
	"math"
	"testing"

	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"
)

// numericIDSamples is how many resources each id test creates. Ids are FNV
// hashes of a random driver id, so on the unmasked code each has a 1-in-2
// chance of setting the top bit; 16 of them all missing it is a 1-in-65536
// event.
const numericIDSamples = 16

// TestSDKGCPForwardingRuleIDsFitInt64 proves every forwarding-rule id and
// pscConnectionId fits in an int64. Terraform's google provider reads
// forwarding_rule_id as an int, and a full-range uint64 id failed the read
// with "expected type 'int', got unconvertible type 'string'".
func TestSDKGCPForwardingRuleIDsFitInt64(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()
	client := newForwardingRulesClient(t, ts.URL, option.WithHTTPClient(ts.Client()))

	for i := range numericIDSamples {
		name := fmt.Sprintf("pscid%d", i)

		op, err := client.Insert(ctx, &computepb.InsertGlobalForwardingRuleRequest{
			Project: testProject,
			ForwardingRuleResource: &computepb.ForwardingRule{
				Name:      ptrStr(name),
				Network:   ptrStr(pscNetwork),
				IPAddress: ptrStr(fmt.Sprintf("10.3.0.%d", i+1)),
				Target:    ptrStr("all-apis"),
			},
		})
		if err != nil {
			t.Fatalf("Insert %s: %v", name, err)
		}

		if err := op.Wait(ctx); err != nil {
			t.Fatalf("Insert %s wait: %v", name, err)
		}

		got, err := client.Get(ctx, &computepb.GetGlobalForwardingRuleRequest{Project: testProject, ForwardingRule: name})
		if err != nil {
			t.Fatalf("Get %s: %v", name, err)
		}

		if got.GetId() == 0 || got.GetId() > math.MaxInt64 {
			t.Errorf("%s: id = %d, want a non-zero value <= MaxInt64", name, got.GetId())
		}

		if got.GetPscConnectionId() == 0 || got.GetPscConnectionId() > math.MaxInt64 {
			t.Errorf("%s: pscConnectionId = %d, want a non-zero value <= MaxInt64", name, got.GetPscConnectionId())
		}
	}
}
