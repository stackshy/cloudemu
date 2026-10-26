package eventbridge_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
)

const sdkSource = "aws.partner/acme.com/app/events"

func sourceState(t *testing.T, eb *awseb.Client) ebtypes.EventSourceState {
	t.Helper()

	out, err := eb.DescribeEventSource(context.Background(), &awseb.DescribeEventSourceInput{Name: aws.String(sdkSource)})
	if err != nil {
		t.Fatalf("DescribeEventSource: %v", err)
	}

	if aws.ToString(out.CreatedBy) != "acme.com" || aws.ToString(out.Arn) != "arn:aws:events:us-east-1::event-source/"+sdkSource ||
		out.CreationTime == nil {
		t.Fatalf("DescribeEventSource = %+v", out)
	}

	return out.State
}

// TestSDKPartnerEventSourceFlow drives the partner and customer sides through
// the real SDK: create the source, match a partner event bus to it, deliver a
// partner event to SQS, then deactivate, activate and delete it.
func TestSDKPartnerEventSourceFlow(t *testing.T) {
	eb, sqs := newEBAndSQS(t)
	ctx := context.Background()

	created, err := eb.CreatePartnerEventSource(ctx, &awseb.CreatePartnerEventSourceInput{
		Name: aws.String(sdkSource), Account: aws.String("123456789012"),
	})
	if err != nil {
		t.Fatalf("CreatePartnerEventSource: %v", err)
	}

	if aws.ToString(created.EventSourceArn) != "arn:aws:events:us-east-1::event-source/"+sdkSource {
		t.Fatalf("EventSourceArn = %q", aws.ToString(created.EventSourceArn))
	}

	_, err = eb.CreatePartnerEventSource(ctx, &awseb.CreatePartnerEventSourceInput{
		Name: aws.String(sdkSource), Account: aws.String("123456789012"),
	})
	requireAPIError(t, err, "ResourceAlreadyExistsException", "Event source "+sdkSource+" already exists.")

	listed, err := eb.ListEventSources(ctx, &awseb.ListEventSourcesInput{NamePrefix: aws.String("aws.partner/acme")})
	if err != nil || len(listed.EventSources) != 1 || listed.EventSources[0].State != ebtypes.EventSourceStatePending ||
		listed.EventSources[0].ExpirationTime == nil {
		t.Fatalf("ListEventSources = %+v, %v", listed, err)
	}

	_, err = eb.CreateEventBus(ctx, &awseb.CreateEventBusInput{Name: aws.String("custom"), EventSourceName: aws.String(sdkSource)})
	requireAPIError(t, err, "ValidationException", "")

	if _, err = eb.CreateEventBus(ctx, &awseb.CreateEventBusInput{
		Name: aws.String(sdkSource), EventSourceName: aws.String(sdkSource),
	}); err != nil {
		t.Fatalf("CreateEventBus: %v", err)
	}

	if s := sourceState(t, eb); s != ebtypes.EventSourceStateActive {
		t.Fatalf("state after CreateEventBus = %s", s)
	}

	url, queueARN := makeQueue(t, sqs, "partner-q")

	if _, err = eb.PutRule(ctx, &awseb.PutRuleInput{
		Name: aws.String("r"), EventBusName: aws.String(sdkSource),
		EventPattern: aws.String(`{"source":[{"prefix":"aws.partner/"}]}`),
	}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	if _, err = eb.PutTargets(ctx, &awseb.PutTargetsInput{
		Rule: aws.String("r"), EventBusName: aws.String(sdkSource),
		Targets: []ebtypes.Target{{Id: aws.String("q"), Arn: aws.String(queueARN)}},
	}); err != nil {
		t.Fatalf("PutTargets: %v", err)
	}

	putPartner(t, eb)

	body, n := receiveOne(t, sqs, url)
	if n != 1 {
		t.Fatalf("got %d messages", n)
	}

	var env map[string]any
	if err = json.Unmarshal([]byte(body), &env); err != nil || env["source"] != sdkSource || env["detail-type"] != "t" {
		t.Fatalf("delivered %s (%v)", body, err)
	}

	if _, err = eb.DeactivateEventSource(ctx, &awseb.DeactivateEventSourceInput{Name: aws.String(sdkSource)}); err != nil {
		t.Fatalf("DeactivateEventSource: %v", err)
	}

	if s := sourceState(t, eb); s != ebtypes.EventSourceStatePending {
		t.Fatalf("state after Deactivate = %s", s)
	}

	putPartner(t, eb)

	if _, n = receiveOne(t, sqs, url); n != 0 {
		t.Fatalf("a PENDING source delivered %d messages", n)
	}

	if _, err = eb.ActivateEventSource(ctx, &awseb.ActivateEventSourceInput{Name: aws.String(sdkSource)}); err != nil {
		t.Fatalf("ActivateEventSource: %v", err)
	}

	if _, err = eb.DeletePartnerEventSource(ctx, &awseb.DeletePartnerEventSourceInput{
		Name: aws.String(sdkSource), Account: aws.String("123456789012"),
	}); err != nil {
		t.Fatalf("DeletePartnerEventSource: %v", err)
	}

	if s := sourceState(t, eb); s != ebtypes.EventSourceStateDeleted {
		t.Fatalf("state after delete = %s", s)
	}

	_, err = eb.ActivateEventSource(ctx, &awseb.ActivateEventSourceInput{Name: aws.String(sdkSource)})
	requireAPIError(t, err, "InvalidStateException", "Event source "+sdkSource+" is in state DELETED.")
}

func putPartner(t *testing.T, eb *awseb.Client) {
	t.Helper()

	out, err := eb.PutPartnerEvents(context.Background(), &awseb.PutPartnerEventsInput{
		Entries: []ebtypes.PutPartnerEventsRequestEntry{{
			Source: aws.String(sdkSource), DetailType: aws.String("t"), Detail: aws.String(`{"a":1}`),
		}},
	})
	if err != nil || out.FailedEntryCount != 0 || len(out.Entries) != 1 || aws.ToString(out.Entries[0].EventId) == "" {
		t.Fatalf("PutPartnerEvents = %+v, %v", out, err)
	}
}

func TestSDKPartnerEventSourcePartnerSide(t *testing.T) {
	eb, _ := newEBAndSQS(t)
	ctx := context.Background()

	for _, acct := range []string{"123456789012", "210987654321"} {
		if _, err := eb.CreatePartnerEventSource(ctx, &awseb.CreatePartnerEventSourceInput{
			Name: aws.String(sdkSource), Account: aws.String(acct),
		}); err != nil {
			t.Fatalf("CreatePartnerEventSource %s: %v", acct, err)
		}
	}

	desc, err := eb.DescribePartnerEventSource(ctx, &awseb.DescribePartnerEventSourceInput{Name: aws.String(sdkSource)})
	if err != nil || aws.ToString(desc.Name) != sdkSource {
		t.Fatalf("DescribePartnerEventSource = %+v, %v", desc, err)
	}

	list, err := eb.ListPartnerEventSources(ctx, &awseb.ListPartnerEventSourcesInput{NamePrefix: aws.String("aws.partner/acme.com/")})
	if err != nil || len(list.PartnerEventSources) != 1 {
		t.Fatalf("ListPartnerEventSources = %+v, %v", list, err)
	}

	accts, err := eb.ListPartnerEventSourceAccounts(ctx, &awseb.ListPartnerEventSourceAccountsInput{
		EventSourceName: aws.String(sdkSource), Limit: aws.Int32(1),
	})
	if err != nil || len(accts.PartnerEventSourceAccounts) != 1 || accts.NextToken == nil ||
		aws.ToString(accts.PartnerEventSourceAccounts[0].Account) != "123456789012" ||
		accts.PartnerEventSourceAccounts[0].State != ebtypes.EventSourceStatePending {
		t.Fatalf("ListPartnerEventSourceAccounts = %+v, %v", accts, err)
	}

	_, err = eb.DescribePartnerEventSource(ctx, &awseb.DescribePartnerEventSourceInput{Name: aws.String("aws.partner/acme.com/none")})
	requireAPIError(t, err, "ResourceNotFoundException", "")

	_, err = eb.DescribeEventSource(ctx, &awseb.DescribeEventSourceInput{Name: aws.String("aws.partner/acme.com/none")})
	requireAPIError(t, err, "ResourceNotFoundException", "")

	_, err = eb.CreatePartnerEventSource(ctx, &awseb.CreatePartnerEventSourceInput{
		Name: aws.String("acme"), Account: aws.String("1"),
	})
	requireAPIError(t, err, "ValidationException", "")

	_, err = eb.CreateEventBus(ctx, &awseb.CreateEventBusInput{
		Name: aws.String("aws.partner/acme.com/none"), EventSourceName: aws.String("aws.partner/acme.com/none"),
	})
	requireAPIError(t, err, "ResourceNotFoundException", "")

	_, err = eb.PutPartnerEvents(ctx, &awseb.PutPartnerEventsInput{Entries: make([]ebtypes.PutPartnerEventsRequestEntry, 21)})
	requireAPIError(t, err, "ValidationException", "")
}
