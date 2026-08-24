package eventbridge_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	"github.com/aws/smithy-go"
)

// TestSDKPutEventsTooManyEntries covers finding 7: PutEvents caps a request at 10
// entries; an 11-entry batch is rejected wholesale with a ValidationException.
func TestSDKPutEventsTooManyEntries(t *testing.T) {
	client := newEventBridgeClient(t)
	ctx := context.Background()

	entries := make([]ebtypes.PutEventsRequestEntry, 11)
	for i := range entries {
		entries[i] = ebtypes.PutEventsRequestEntry{
			Source:     aws.String("com.example"),
			DetailType: aws.String("test"),
			Detail:     aws.String(fmt.Sprintf(`{"i":%d}`, i)),
		}
	}

	_, err := client.PutEvents(ctx, &awseb.PutEventsInput{Entries: entries})
	if err == nil {
		t.Fatal("PutEvents with 11 entries succeeded, want ValidationException")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not a smithy.APIError: %v", err)
	}

	if apiErr.ErrorCode() != "ValidationException" {
		t.Fatalf("error code = %q, want ValidationException", apiErr.ErrorCode())
	}
}

// TestSDKPutEventsAtLimit guards the happy path: exactly 10 entries are accepted.
func TestSDKPutEventsAtLimit(t *testing.T) {
	client := newEventBridgeClient(t)
	ctx := context.Background()

	entries := make([]ebtypes.PutEventsRequestEntry, 10)
	for i := range entries {
		entries[i] = ebtypes.PutEventsRequestEntry{
			Source:     aws.String("com.example"),
			DetailType: aws.String("test"),
			Detail:     aws.String(fmt.Sprintf(`{"i":%d}`, i)),
		}
	}

	out, err := client.PutEvents(ctx, &awseb.PutEventsInput{Entries: entries})
	if err != nil {
		t.Fatalf("PutEvents with 10 entries: %v", err)
	}

	if out.FailedEntryCount != 0 || len(out.Entries) != 10 {
		t.Fatalf("FailedEntryCount=%d entries=%d, want 0 and 10", out.FailedEntryCount, len(out.Entries))
	}
}
