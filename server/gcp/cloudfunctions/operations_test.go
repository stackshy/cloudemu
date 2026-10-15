package cloudfunctions_test

import (
	"context"
	"testing"

	functionsv2 "cloud.google.com/go/functions/apiv2"
	v2pb "cloud.google.com/go/functions/apiv2/functionspb"
)

// TestGapicGen2OperationResume pins GCF-04: a functions/apiv2 client that
// resumes CreateFunction by operation name polls the stored operation and gets
// the Function back, while resuming a name that was never minted fails with
// 404 instead of a fabricated done operation.
func TestGapicGen2OperationResume(t *testing.T) {
	ctx := context.Background()

	c, err := functionsv2.NewFunctionRESTClient(ctx, gapicOptions(t)...)
	if err != nil {
		t.Fatalf("NewFunctionRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = c.Close() })

	op, err := c.CreateFunction(ctx, &v2pb.CreateFunctionRequest{
		Parent:     gapicParent,
		FunctionId: "resumed",
		Function: &v2pb.Function{
			BuildConfig: &v2pb.BuildConfig{Runtime: "go121", EntryPoint: "Hello"},
		},
	})
	if err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	fn, err := c.CreateFunctionOperation(op.Name()).Wait(ctx)
	if err != nil {
		t.Fatalf("resumed Wait(%s): %v", op.Name(), err)
	}

	if fn.GetName() != gapicParent+"/functions/resumed" {
		t.Fatalf("resumed Wait returned %q", fn.GetName())
	}

	_, err = c.CreateFunctionOperation(gapicParent + "/operations/cf2-nope").Wait(ctx)
	assertNotFound(t, "unknown op Wait", err)
}
