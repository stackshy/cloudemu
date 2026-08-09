package sesv2_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsses "github.com/aws/aws-sdk-go-v2/service/sesv2"
	sestypes "github.com/aws/aws-sdk-go-v2/service/sesv2/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newSESClient(t *testing.T) *awsses.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{SESV2: cloud.SESV2})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awsses.NewFromConfig(cfg, func(o *awsses.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func TestSDKEmailIdentityLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newSESClient(t)

	created, err := c.CreateEmailIdentity(ctx, &awsses.CreateEmailIdentityInput{
		EmailIdentity: aws.String("sender@example.com"),
	})
	if err != nil {
		t.Fatalf("CreateEmailIdentity: %v", err)
	}

	if !created.VerifiedForSendingStatus {
		t.Fatal("identity should auto-verify for sending")
	}

	got, err := c.GetEmailIdentity(ctx, &awsses.GetEmailIdentityInput{
		EmailIdentity: aws.String("sender@example.com"),
	})
	if err != nil {
		t.Fatalf("GetEmailIdentity: %v", err)
	}

	if got.VerificationStatus != sestypes.VerificationStatusSuccess {
		t.Fatalf("verification status = %s, want SUCCESS", got.VerificationStatus)
	}

	list, err := c.ListEmailIdentities(ctx, &awsses.ListEmailIdentitiesInput{})
	if err != nil || len(list.EmailIdentities) != 1 {
		t.Fatalf("ListEmailIdentities = %d, %v", len(list.EmailIdentities), err)
	}

	if _, err := c.DeleteEmailIdentity(ctx, &awsses.DeleteEmailIdentityInput{
		EmailIdentity: aws.String("sender@example.com"),
	}); err != nil {
		t.Fatalf("DeleteEmailIdentity: %v", err)
	}
}

func TestSDKGetEmailIdentityNotFound(t *testing.T) {
	ctx := context.Background()
	c := newSESClient(t)

	_, err := c.GetEmailIdentity(ctx, &awsses.GetEmailIdentityInput{EmailIdentity: aws.String("missing@example.com")})
	if err == nil {
		t.Fatal("expected error for missing identity")
	}

	var nf *sestypes.NotFoundException
	if !errors.As(err, &nf) {
		t.Fatalf("want NotFoundException, got %T: %v", err, err)
	}
}

func TestSDKCreateIdentityDuplicate(t *testing.T) {
	ctx := context.Background()
	c := newSESClient(t)

	in := &awsses.CreateEmailIdentityInput{EmailIdentity: aws.String("dup@example.com")}

	if _, err := c.CreateEmailIdentity(ctx, in); err != nil {
		t.Fatalf("first create: %v", err)
	}

	_, err := c.CreateEmailIdentity(ctx, in)
	if err == nil {
		t.Fatal("expected AlreadyExistsException on duplicate")
	}

	var ae *sestypes.AlreadyExistsException
	if !errors.As(err, &ae) {
		t.Fatalf("want AlreadyExistsException, got %T: %v", err, err)
	}
}

func TestSDKSendEmail(t *testing.T) {
	ctx := context.Background()
	c := newSESClient(t)

	if _, err := c.CreateEmailIdentity(ctx, &awsses.CreateEmailIdentityInput{
		EmailIdentity: aws.String("sender@example.com"),
	}); err != nil {
		t.Fatalf("create identity: %v", err)
	}

	out, err := c.SendEmail(ctx, &awsses.SendEmailInput{
		FromEmailAddress: aws.String("sender@example.com"),
		Destination:      &sestypes.Destination{ToAddresses: []string{"dest@elsewhere.com"}},
		Content: &sestypes.EmailContent{
			Simple: &sestypes.Message{
				Subject: &sestypes.Content{Data: aws.String("Hello")},
				Body:    &sestypes.Body{Text: &sestypes.Content{Data: aws.String("Body text")}},
			},
		},
	})
	if err != nil {
		t.Fatalf("SendEmail: %v", err)
	}

	if aws.ToString(out.MessageId) == "" {
		t.Fatal("expected a non-empty MessageId")
	}
}

func TestSDKSendEmailUnverified(t *testing.T) {
	ctx := context.Background()
	c := newSESClient(t)

	_, err := c.SendEmail(ctx, &awsses.SendEmailInput{
		FromEmailAddress: aws.String("nobody@example.com"),
		Destination:      &sestypes.Destination{ToAddresses: []string{"dest@example.com"}},
		Content: &sestypes.EmailContent{
			Simple: &sestypes.Message{
				Subject: &sestypes.Content{Data: aws.String("x")},
				Body:    &sestypes.Body{Text: &sestypes.Content{Data: aws.String("y")}},
			},
		},
	})
	if err == nil {
		t.Fatal("expected error sending from unverified identity")
	}

	var nf *sestypes.NotFoundException
	if !errors.As(err, &nf) {
		t.Fatalf("want NotFoundException, got %T: %v", err, err)
	}
}

func TestSDKConfigurationSetLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newSESClient(t)

	if _, err := c.CreateConfigurationSet(ctx, &awsses.CreateConfigurationSetInput{
		ConfigurationSetName: aws.String("cs1"),
		SendingOptions:       &sestypes.SendingOptions{SendingEnabled: true},
	}); err != nil {
		t.Fatalf("CreateConfigurationSet: %v", err)
	}

	got, err := c.GetConfigurationSet(ctx, &awsses.GetConfigurationSetInput{ConfigurationSetName: aws.String("cs1")})
	if err != nil {
		t.Fatalf("GetConfigurationSet: %v", err)
	}

	if got.SendingOptions == nil || !got.SendingOptions.SendingEnabled {
		t.Fatalf("sending should be enabled, got %+v", got.SendingOptions)
	}

	list, err := c.ListConfigurationSets(ctx, &awsses.ListConfigurationSetsInput{})
	if err != nil || len(list.ConfigurationSets) != 1 {
		t.Fatalf("ListConfigurationSets = %v, %v", list.ConfigurationSets, err)
	}

	if _, err := c.DeleteConfigurationSet(ctx, &awsses.DeleteConfigurationSetInput{
		ConfigurationSetName: aws.String("cs1"),
	}); err != nil {
		t.Fatalf("DeleteConfigurationSet: %v", err)
	}
}

func TestSDKTemplateRender(t *testing.T) {
	ctx := context.Background()
	c := newSESClient(t)

	if _, err := c.CreateEmailTemplate(ctx, &awsses.CreateEmailTemplateInput{
		TemplateName: aws.String("welcome"),
		TemplateContent: &sestypes.EmailTemplateContent{
			Subject: aws.String("Hi {{name}}"),
			Text:    aws.String("Welcome {{name}}"),
		},
	}); err != nil {
		t.Fatalf("CreateEmailTemplate: %v", err)
	}

	rendered, err := c.TestRenderEmailTemplate(ctx, &awsses.TestRenderEmailTemplateInput{
		TemplateName: aws.String("welcome"),
		TemplateData: aws.String(`{"name":"Ada"}`),
	})
	if err != nil {
		t.Fatalf("TestRenderEmailTemplate: %v", err)
	}

	if got := aws.ToString(rendered.RenderedTemplate); got != "Subject: Hi Ada\n\nWelcome Ada" {
		t.Fatalf("rendered = %q", got)
	}

	if _, err := c.UpdateEmailTemplate(ctx, &awsses.UpdateEmailTemplateInput{
		TemplateName:    aws.String("welcome"),
		TemplateContent: &sestypes.EmailTemplateContent{Subject: aws.String("Yo {{name}}"), Text: aws.String("Hey {{name}}")},
	}); err != nil {
		t.Fatalf("UpdateEmailTemplate: %v", err)
	}

	got, err := c.GetEmailTemplate(ctx, &awsses.GetEmailTemplateInput{TemplateName: aws.String("welcome")})
	if err != nil || aws.ToString(got.TemplateContent.Subject) != "Yo {{name}}" {
		t.Fatalf("GetEmailTemplate after update: %v %+v", err, got.TemplateContent)
	}

	list, err := c.ListEmailTemplates(ctx, &awsses.ListEmailTemplatesInput{})
	if err != nil || len(list.TemplatesMetadata) != 1 {
		t.Fatalf("ListEmailTemplates = %v, %v", list.TemplatesMetadata, err)
	}

	if _, err := c.DeleteEmailTemplate(ctx, &awsses.DeleteEmailTemplateInput{TemplateName: aws.String("welcome")}); err != nil {
		t.Fatalf("DeleteEmailTemplate: %v", err)
	}
}

func TestSDKSuppressionList(t *testing.T) {
	ctx := context.Background()
	c := newSESClient(t)

	if _, err := c.PutSuppressedDestination(ctx, &awsses.PutSuppressedDestinationInput{
		EmailAddress: aws.String("bounce@example.com"),
		Reason:       sestypes.SuppressionListReasonBounce,
	}); err != nil {
		t.Fatalf("PutSuppressedDestination: %v", err)
	}

	got, err := c.GetSuppressedDestination(ctx, &awsses.GetSuppressedDestinationInput{
		EmailAddress: aws.String("bounce@example.com"),
	})
	if err != nil {
		t.Fatalf("GetSuppressedDestination: %v", err)
	}

	if got.SuppressedDestination.Reason != sestypes.SuppressionListReasonBounce {
		t.Fatalf("reason = %s, want BOUNCE", got.SuppressedDestination.Reason)
	}

	list, err := c.ListSuppressedDestinations(ctx, &awsses.ListSuppressedDestinationsInput{})
	if err != nil || len(list.SuppressedDestinationSummaries) != 1 {
		t.Fatalf("ListSuppressedDestinations = %d, %v", len(list.SuppressedDestinationSummaries), err)
	}

	if _, err := c.DeleteSuppressedDestination(ctx, &awsses.DeleteSuppressedDestinationInput{
		EmailAddress: aws.String("bounce@example.com"),
	}); err != nil {
		t.Fatalf("DeleteSuppressedDestination: %v", err)
	}
}

func TestSDKGetAccount(t *testing.T) {
	ctx := context.Background()
	c := newSESClient(t)

	got, err := c.GetAccount(ctx, &awsses.GetAccountInput{})
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}

	if !got.SendingEnabled {
		t.Fatal("sending should be enabled by default")
	}

	if got.SendQuota == nil || got.SendQuota.Max24HourSend == 0 {
		t.Fatalf("expected a send quota, got %+v", got.SendQuota)
	}

	if _, err := c.PutAccountSendingAttributes(ctx, &awsses.PutAccountSendingAttributesInput{
		SendingEnabled: false,
	}); err != nil {
		t.Fatalf("PutAccountSendingAttributes: %v", err)
	}

	got, _ = c.GetAccount(ctx, &awsses.GetAccountInput{})
	if got.SendingEnabled {
		t.Fatal("sending should be disabled after PutAccountSendingAttributes")
	}
}

func TestSDKTagResource(t *testing.T) {
	ctx := context.Background()
	c := newSESClient(t)

	if _, err := c.CreateEmailIdentity(ctx, &awsses.CreateEmailIdentityInput{
		EmailIdentity: aws.String("tagged@example.com"),
	}); err != nil {
		t.Fatalf("create identity: %v", err)
	}

	arn := "arn:aws:ses:us-east-1:123456789012:identity/tagged@example.com"

	if _, err := c.TagResource(ctx, &awsses.TagResourceInput{
		ResourceArn: aws.String(arn),
		Tags:        []sestypes.Tag{{Key: aws.String("env"), Value: aws.String("prod")}},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	got, err := c.ListTagsForResource(ctx, &awsses.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	if err != nil || len(got.Tags) != 1 || aws.ToString(got.Tags[0].Value) != "prod" {
		t.Fatalf("ListTagsForResource: %v %+v", err, got.Tags)
	}

	if _, err := c.UntagResource(ctx, &awsses.UntagResourceInput{
		ResourceArn: aws.String(arn),
		TagKeys:     []string{"env"},
	}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	got, _ = c.ListTagsForResource(ctx, &awsses.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	if len(got.Tags) != 0 {
		t.Fatalf("tags should be empty, got %+v", got.Tags)
	}
}
