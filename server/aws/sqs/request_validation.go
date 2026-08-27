package sqs

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/stackshy/cloudemu/v2/server/wire"
)

// maxBatchEntries is the maximum number of entries SQS accepts in a single
// SendMessageBatch/DeleteMessageBatch/ChangeMessageVisibilityBatch request.
const maxBatchEntries = 10

// SQS numeric queue-attribute ranges (inclusive), per the SQS API reference.
const (
	minVisibilityTimeout = 0
	maxVisibilityTimeout = 43200
	minDelaySeconds      = 0
	maxDelaySeconds      = 900
	minRetentionPeriod   = 60
	maxRetentionPeriod   = 1209600
	minMessageSizeBytes  = 1024
	maxMessageSizeBytes  = 1048576
	minWaitTimeSeconds   = 0
	maxWaitTimeSeconds   = 20
)

// validateBatchEntryIDs enforces the structural constraints SQS applies to every
// batch request before any entry is processed: the batch must be non-empty, hold
// at most 10 entries, and use distinct entry Ids. It writes the matching SQS
// error (EmptyBatchRequest / TooManyEntriesInBatchRequest / BatchEntryIdsNotDistinct)
// and reports false when the batch is rejected, so callers enqueue nothing.
func validateBatchEntryIDs(w http.ResponseWriter, ids []string) bool {
	if len(ids) == 0 {
		wire.WriteJSONError(w, http.StatusBadRequest, "EmptyBatchRequest",
			"The batch request doesn't contain any entries.")

		return false
	}

	if len(ids) > maxBatchEntries {
		wire.WriteJSONError(w, http.StatusBadRequest, "TooManyEntriesInBatchRequest",
			fmt.Sprintf("Maximum number of entries per request are %d. You have sent %d.", maxBatchEntries, len(ids)))

		return false
	}

	seen := make(map[string]struct{}, len(ids))

	for _, id := range ids {
		if _, dup := seen[id]; dup {
			wire.WriteJSONError(w, http.StatusBadRequest, "BatchEntryIdsNotDistinct",
				fmt.Sprintf("Id %s repeated.", id))

			return false
		}

		seen[id] = struct{}{}
	}

	return true
}

// validateQueueAttributeRanges rejects out-of-range numeric queue attributes with
// InvalidAttributeValue (HTTP 400), matching real SQS CreateQueue/SetQueueAttributes.
// Only the numeric attributes present in the map are checked; absent attributes
// and non-numeric attributes are ignored here. It reports false when rejected.
func validateQueueAttributeRanges(w http.ResponseWriter, attrs map[string]string) bool {
	ranges := []struct {
		name     string
		min, max int
	}{
		{"VisibilityTimeout", minVisibilityTimeout, maxVisibilityTimeout},
		{"DelaySeconds", minDelaySeconds, maxDelaySeconds},
		{"MessageRetentionPeriod", minRetentionPeriod, maxRetentionPeriod},
		{"MaximumMessageSize", minMessageSizeBytes, maxMessageSizeBytes},
		{"ReceiveMessageWaitTimeSeconds", minWaitTimeSeconds, maxWaitTimeSeconds},
	}

	for _, rng := range ranges {
		raw, ok := attrs[rng.name]
		if !ok {
			continue
		}

		n, err := strconv.Atoi(raw)
		if err != nil || n < rng.min || n > rng.max {
			wire.WriteJSONError(w, http.StatusBadRequest, "InvalidAttributeValue",
				fmt.Sprintf("Invalid value for the parameter %s.", rng.name))

			return false
		}
	}

	return true
}
