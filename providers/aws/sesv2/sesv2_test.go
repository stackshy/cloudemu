package sesv2_test

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/sesv2"
	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

func newMock(t *testing.T) *sesv2.Mock {
	t.Helper()

	return sesv2.New(config.NewOptions(
		config.WithClock(config.NewFakeClock(time.Unix(0, 0))),
		config.WithRegion("us-east-1"),
		config.WithAccountID("000000000000"),
	))
}

func TestCreateEmailIdentityAutoVerifies(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	id, err := m.CreateEmailIdentity(ctx, driver.CreateIdentityInput{EmailIdentity: "sender@example.com"})
	if err != nil {
		t.Fatalf("CreateEmailIdentity: %v", err)
	}

	if id.VerificationStatus != driver.StatusSuccess || !id.VerifiedForSendingStatus {
		t.Fatalf("identity should auto-verify, got %+v", id)
	}

	if id.Type != driver.IdentityTypeEmailAddress {
		t.Fatalf("want EMAIL_ADDRESS, got %s", id.Type)
	}
}

func TestCreateDomainIdentityGetsDkimTokens(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	id, err := m.CreateEmailIdentity(ctx, driver.CreateIdentityInput{EmailIdentity: "example.com"})
	if err != nil {
		t.Fatalf("CreateEmailIdentity: %v", err)
	}

	if id.Type != driver.IdentityTypeDomain {
		t.Fatalf("want DOMAIN, got %s", id.Type)
	}

	if len(id.DkimTokens) != 3 {
		t.Fatalf("domain identity should have 3 DKIM tokens, got %d", len(id.DkimTokens))
	}
}

func TestCreateEmailIdentityRequiresName(t *testing.T) {
	m := newMock(t)

	if _, err := m.CreateEmailIdentity(context.Background(), driver.CreateIdentityInput{}); !errors.IsInvalidArgument(err) {
		t.Fatalf("empty identity should be InvalidArgument, got %v", err)
	}
}

func TestCreateEmailIdentityDuplicate(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	if _, err := m.CreateEmailIdentity(ctx, driver.CreateIdentityInput{EmailIdentity: "a@example.com"}); err != nil {
		t.Fatalf("first create: %v", err)
	}

	if _, err := m.CreateEmailIdentity(ctx, driver.CreateIdentityInput{EmailIdentity: "a@example.com"}); !errors.IsAlreadyExists(err) {
		t.Fatalf("duplicate should be AlreadyExists, got %v", err)
	}
}

func TestGetDeleteEmailIdentity(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	if _, err := m.CreateEmailIdentity(ctx, driver.CreateIdentityInput{EmailIdentity: "a@example.com"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := m.GetEmailIdentity(ctx, "a@example.com"); err != nil {
		t.Fatalf("get: %v", err)
	}

	if err := m.DeleteEmailIdentity(ctx, "a@example.com"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := m.GetEmailIdentity(ctx, "a@example.com"); !errors.IsNotFound(err) {
		t.Fatalf("get after delete should be NotFound, got %v", err)
	}
}

func TestSendEmailRequiresVerifiedIdentity(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	_, err := m.SendEmail(ctx, driver.SendEmailInput{
		FromAddress: "nobody@example.com",
		ToAddresses: []string{"dest@example.com"},
		Subject:     "hi",
	})
	if !errors.IsNotFound(err) {
		t.Fatalf("send from unverified should be NotFound, got %v", err)
	}
}

func TestSendEmailByDomainIdentity(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	if _, err := m.CreateEmailIdentity(ctx, driver.CreateIdentityInput{EmailIdentity: "example.com"}); err != nil {
		t.Fatalf("create domain: %v", err)
	}

	// The from address belongs to the verified domain, not registered directly.
	msgID, err := m.SendEmail(ctx, driver.SendEmailInput{
		FromAddress: "sender@example.com",
		ToAddresses: []string{"dest@elsewhere.com"},
		Subject:     "hi",
	})
	if err != nil {
		t.Fatalf("SendEmail via domain identity: %v", err)
	}

	if msgID == "" {
		t.Fatal("expected a non-empty MessageId")
	}

	sent := m.ListSentMessages(ctx)
	if len(sent) != 1 || sent[0].MessageID != msgID {
		t.Fatalf("sent message not recorded: %+v", sent)
	}
}

func TestSendEmailRequiresDestination(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	if _, err := m.CreateEmailIdentity(ctx, driver.CreateIdentityInput{EmailIdentity: "sender@example.com"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err := m.SendEmail(ctx, driver.SendEmailInput{FromAddress: "sender@example.com", Subject: "x"})
	if !errors.IsInvalidArgument(err) {
		t.Fatalf("no destination should be InvalidArgument, got %v", err)
	}
}

func TestConfigurationSetLifecycle(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	if err := m.CreateConfigurationSet(ctx, driver.CreateConfigurationSetInput{Name: "cs1", SendingEnabled: true}); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := m.CreateConfigurationSet(ctx, driver.CreateConfigurationSetInput{Name: "cs1"}); !errors.IsAlreadyExists(err) {
		t.Fatalf("duplicate should be AlreadyExists, got %v", err)
	}

	cs, err := m.GetConfigurationSet(ctx, "cs1")
	if err != nil || !cs.SendingEnabled {
		t.Fatalf("get: %v %+v", err, cs)
	}

	names, err := m.ListConfigurationSets(ctx)
	if err != nil || len(names) != 1 || names[0] != "cs1" {
		t.Fatalf("list: %v %v", err, names)
	}

	if err := m.DeleteConfigurationSet(ctx, "cs1"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := m.GetConfigurationSet(ctx, "cs1"); !errors.IsNotFound(err) {
		t.Fatalf("get after delete should be NotFound, got %v", err)
	}
}

func TestTemplateRenderAndUpdate(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	in := driver.TemplateInput{
		Name: "welcome",
		Content: driver.TemplateContent{
			Subject: "Hi {{name}}",
			HTML:    "<p>Welcome {{name}}</p>",
		},
	}
	if err := m.CreateEmailTemplate(ctx, in); err != nil {
		t.Fatalf("create: %v", err)
	}

	rendered, err := m.TestRenderEmailTemplate(ctx, "welcome", `{"name":"Ada"}`)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	want := "Subject: Hi Ada\n\n<p>Welcome Ada</p>"
	if rendered != want {
		t.Fatalf("render = %q, want %q", rendered, want)
	}

	in.Content.HTML = "<p>Hello {{name}}</p>"
	if err := m.UpdateEmailTemplate(ctx, in); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := m.GetEmailTemplate(ctx, "welcome")
	if err != nil || got.Content.HTML != "<p>Hello {{name}}</p>" {
		t.Fatalf("get after update: %v %+v", err, got)
	}
}

func TestSuppressionList(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	if err := m.PutSuppressedDestination(ctx, driver.PutSuppressedInput{
		EmailAddress: "bounce@example.com",
		Reason:       driver.SuppressionReasonBounce,
	}); err != nil {
		t.Fatalf("put: %v", err)
	}

	s, err := m.GetSuppressedDestination(ctx, "bounce@example.com")
	if err != nil || s.Reason != driver.SuppressionReasonBounce {
		t.Fatalf("get: %v %+v", err, s)
	}

	list, err := m.ListSuppressedDestinations(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %v", err, list)
	}

	if err := m.DeleteSuppressedDestination(ctx, "bounce@example.com"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := m.GetSuppressedDestination(ctx, "bounce@example.com"); !errors.IsNotFound(err) {
		t.Fatalf("get after delete should be NotFound, got %v", err)
	}
}

func TestAccountAttributes(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	acct, err := m.GetAccount(ctx)
	if err != nil || !acct.SendingEnabled {
		t.Fatalf("get account: %v %+v", err, acct)
	}

	if err := m.PutAccountSendingAttributes(ctx, false); err != nil {
		t.Fatalf("put sending: %v", err)
	}

	acct, _ = m.GetAccount(ctx)
	if acct.SendingEnabled {
		t.Fatal("sending should be disabled")
	}

	if err := m.PutAccountSuppressionAttributes(ctx, []string{driver.SuppressionReasonComplaint}); err != nil {
		t.Fatalf("put suppression: %v", err)
	}

	acct, _ = m.GetAccount(ctx)
	if len(acct.SuppressedReasons) != 1 || acct.SuppressedReasons[0] != driver.SuppressionReasonComplaint {
		t.Fatalf("unexpected suppressed reasons: %v", acct.SuppressedReasons)
	}
}

func TestTagsOnIdentity(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	if _, err := m.CreateEmailIdentity(ctx, driver.CreateIdentityInput{EmailIdentity: "a@example.com"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	arn := "arn:aws:ses:us-east-1:000000000000:identity/a@example.com"

	if err := m.TagResource(ctx, arn, map[string]string{"team": "eng"}); err != nil {
		t.Fatalf("tag: %v", err)
	}

	tags, err := m.ListTagsForResource(ctx, arn)
	if err != nil || tags["team"] != "eng" {
		t.Fatalf("list tags: %v %v", err, tags)
	}

	if err := m.UntagResource(ctx, arn, []string{"team"}); err != nil {
		t.Fatalf("untag: %v", err)
	}

	tags, _ = m.ListTagsForResource(ctx, arn)
	if len(tags) != 0 {
		t.Fatalf("tags should be empty after untag, got %v", tags)
	}
}

func TestTagResourceInvalidARN(t *testing.T) {
	m := newMock(t)

	if err := m.TagResource(context.Background(), "not-an-arn", map[string]string{"k": "v"}); !errors.IsInvalidArgument(err) {
		t.Fatalf("invalid ARN should be InvalidArgument, got %v", err)
	}
}

func TestExportJobCancel(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	jobID, err := m.CreateExportJob(ctx)
	if err != nil {
		t.Fatalf("CreateExportJob: %v", err)
	}

	if err := m.CancelExportJob(ctx, jobID); err != nil {
		t.Fatalf("CancelExportJob: %v", err)
	}

	job, err := m.GetExportJob(ctx, jobID)
	if err != nil || job.Status != driver.JobStatusCancelled {
		t.Fatalf("job should be cancelled, got %+v %v", job, err)
	}

	if err := m.CancelExportJob(ctx, "missing"); !errors.IsNotFound(err) {
		t.Fatalf("missing job should be NotFound, got %v", err)
	}
}

func TestBatchGetMetricDataZeroed(t *testing.T) {
	m := newMock(t)

	data, err := m.BatchGetMetricData(context.Background(), []string{"q1", "q2"})
	if err != nil {
		t.Fatalf("BatchGetMetricData: %v", err)
	}

	if len(data) != 2 || len(data["q1"]) == 0 {
		t.Fatalf("expected zeroed series per query, got %+v", data)
	}
}

func TestReputationEntityMaterializesHealthy(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	e, err := m.GetReputationEntity(ctx, driver.ReputationEntityTypeResource, "ref1")
	if err != nil || e.CustomerManagedStatus != driver.ReputationStatusHealthy {
		t.Fatalf("expected healthy materialized entity, got %+v %v", e, err)
	}

	if err := m.UpdateReputationEntityCustomerManagedStatus(ctx, driver.ReputationEntityTypeResource, "ref1", "DISABLED"); err != nil {
		t.Fatalf("UpdateReputationEntityCustomerManagedStatus: %v", err)
	}

	e, _ = m.GetReputationEntity(ctx, driver.ReputationEntityTypeResource, "ref1")
	if e.CustomerManagedStatus != "DISABLED" {
		t.Fatalf("status should update, got %s", e.CustomerManagedStatus)
	}

	all, err := m.ListReputationEntities(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("ListReputationEntities = %d, %v", len(all), err)
	}
}
