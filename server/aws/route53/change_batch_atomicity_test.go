package route53_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsr53 "github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
)

// concurrentContenders is the number of goroutines racing to consume the same
// contested record set. It must be large enough that, absent serialization
// between validating a batch and applying it, at least one goroutine would
// observe (and act on) a stale read of the contested record.
const concurrentContenders = 25

// TestSDKChangeResourceRecordSetsBatchAtomicUnderConcurrency proves
// ChangeResourceRecordSets is atomic even when two batches race against the
// same zone: every batch either applies in full or leaves no trace, never
// partially. Each of concurrentContenders goroutines submits a two-change
// batch — CREATE a goroutine-unique record, then DELETE a single shared
// record that only one batch can win — against the same zone at once. Without
// serializing a batch's validate-then-apply against concurrent writers, a
// losing goroutine could have its CREATE applied before its DELETE fails on
// the now-missing shared record, returning an error to the caller while
// leaving its "rolled back" record behind. This locks that exactly one
// goroutine wins, and every loser's record is absent.
func TestSDKChangeResourceRecordSetsBatchAtomicUnderConcurrency(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()
	zoneID := createRoutingZone(t, client, "atomic.com.")

	const contestedName = "contested.atomic.com."

	changeRecordSet(t, client, zoneID, r53types.ChangeActionCreate, &r53types.ResourceRecordSet{
		Name: aws.String(contestedName), Type: r53types.RRTypeA, TTL: aws.Int64(60),
		ResourceRecords: []r53types.ResourceRecord{{Value: aws.String("198.51.100.1")}},
	})

	var wg sync.WaitGroup

	start := make(chan struct{})
	wins := make([]bool, concurrentContenders)

	for i := range concurrentContenders {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()
			<-start

			uniqueName := fmt.Sprintf("racer-%d.atomic.com.", i)

			_, err := client.ChangeResourceRecordSets(ctx, &awsr53.ChangeResourceRecordSetsInput{
				HostedZoneId: aws.String(zoneID),
				ChangeBatch: &r53types.ChangeBatch{Changes: []r53types.Change{
					{
						Action: r53types.ChangeActionCreate,
						ResourceRecordSet: &r53types.ResourceRecordSet{
							Name: aws.String(uniqueName), Type: r53types.RRTypeA, TTL: aws.Int64(60),
							ResourceRecords: []r53types.ResourceRecord{{Value: aws.String("198.51.100.2")}},
						},
					},
					{
						Action: r53types.ChangeActionDelete,
						ResourceRecordSet: &r53types.ResourceRecordSet{
							Name: aws.String(contestedName), Type: r53types.RRTypeA, TTL: aws.Int64(60),
							ResourceRecords: []r53types.ResourceRecord{{Value: aws.String("198.51.100.1")}},
						},
					},
				}},
			})

			wins[i] = err == nil
		}(i)
	}

	close(start)
	wg.Wait()

	rrsets, err := client.ListResourceRecordSets(ctx, &awsr53.ListResourceRecordSetsInput{HostedZoneId: aws.String(zoneID)})
	if err != nil {
		t.Fatalf("ListResourceRecordSets: %v", err)
	}

	present := make(map[string]bool, len(rrsets.ResourceRecordSets))
	for i := range rrsets.ResourceRecordSets {
		present[aws.ToString(rrsets.ResourceRecordSets[i].Name)] = true
	}

	winners := 0

	for i := range concurrentContenders {
		uniqueName := fmt.Sprintf("racer-%d.atomic.com.", i)
		if wins[i] {
			winners++

			if !present[uniqueName] {
				t.Errorf("goroutine %d reported success but its record %q is missing", i, uniqueName)
			}
		} else if present[uniqueName] {
			t.Errorf("goroutine %d reported failure but its record %q was created anyway (partial apply)", i, uniqueName)
		}
	}

	if winners != 1 {
		t.Errorf("winning goroutines = %d, want exactly 1", winners)
	}

	if present[contestedName] {
		t.Errorf("contested record %q should have been deleted by the winner", contestedName)
	}
}
