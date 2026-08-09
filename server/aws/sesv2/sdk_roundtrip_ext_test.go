package sesv2_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsses "github.com/aws/aws-sdk-go-v2/service/sesv2"
	sestypes "github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

func TestSDKContactListLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newSESClient(t)

	if _, err := c.CreateContactList(ctx, &awsses.CreateContactListInput{
		ContactListName: aws.String("news"),
		Description:     aws.String("newsletter"),
		Topics: []sestypes.Topic{{
			TopicName:                 aws.String("weekly"),
			DisplayName:               aws.String("Weekly"),
			DefaultSubscriptionStatus: sestypes.SubscriptionStatusOptIn,
		}},
	}); err != nil {
		t.Fatalf("CreateContactList: %v", err)
	}

	got, err := c.GetContactList(ctx, &awsses.GetContactListInput{ContactListName: aws.String("news")})
	if err != nil || aws.ToString(got.Description) != "newsletter" || len(got.Topics) != 1 {
		t.Fatalf("GetContactList: %v %+v", err, got)
	}

	list, err := c.ListContactLists(ctx, &awsses.ListContactListsInput{})
	if err != nil || len(list.ContactLists) != 1 {
		t.Fatalf("ListContactLists = %d, %v", len(list.ContactLists), err)
	}

	if _, err := c.DeleteContactList(ctx, &awsses.DeleteContactListInput{ContactListName: aws.String("news")}); err != nil {
		t.Fatalf("DeleteContactList: %v", err)
	}
}

func TestSDKContactLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newSESClient(t)

	if _, err := c.CreateContactList(ctx, &awsses.CreateContactListInput{
		ContactListName: aws.String("list1"),
	}); err != nil {
		t.Fatalf("CreateContactList: %v", err)
	}

	if _, err := c.CreateContact(ctx, &awsses.CreateContactInput{
		ContactListName: aws.String("list1"),
		EmailAddress:    aws.String("sub@example.com"),
	}); err != nil {
		t.Fatalf("CreateContact: %v", err)
	}

	got, err := c.GetContact(ctx, &awsses.GetContactInput{
		ContactListName: aws.String("list1"),
		EmailAddress:    aws.String("sub@example.com"),
	})
	if err != nil || aws.ToString(got.EmailAddress) != "sub@example.com" {
		t.Fatalf("GetContact: %v %+v", err, got)
	}

	if _, err := c.UpdateContact(ctx, &awsses.UpdateContactInput{
		ContactListName: aws.String("list1"),
		EmailAddress:    aws.String("sub@example.com"),
		UnsubscribeAll:  true,
	}); err != nil {
		t.Fatalf("UpdateContact: %v", err)
	}

	list, err := c.ListContacts(ctx, &awsses.ListContactsInput{ContactListName: aws.String("list1")})
	if err != nil || len(list.Contacts) != 1 {
		t.Fatalf("ListContacts = %d, %v", len(list.Contacts), err)
	}

	if _, err := c.DeleteContact(ctx, &awsses.DeleteContactInput{
		ContactListName: aws.String("list1"),
		EmailAddress:    aws.String("sub@example.com"),
	}); err != nil {
		t.Fatalf("DeleteContact: %v", err)
	}
}

func TestSDKCustomVerificationTemplateLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newSESClient(t)

	in := &awsses.CreateCustomVerificationEmailTemplateInput{
		TemplateName:          aws.String("verify"),
		FromEmailAddress:      aws.String("from@example.com"),
		TemplateSubject:       aws.String("Verify"),
		TemplateContent:       aws.String("<p>Verify</p>"),
		SuccessRedirectionURL: aws.String("https://ok"),
		FailureRedirectionURL: aws.String("https://no"),
	}
	if _, err := c.CreateCustomVerificationEmailTemplate(ctx, in); err != nil {
		t.Fatalf("CreateCustomVerificationEmailTemplate: %v", err)
	}

	got, err := c.GetCustomVerificationEmailTemplate(ctx, &awsses.GetCustomVerificationEmailTemplateInput{
		TemplateName: aws.String("verify"),
	})
	if err != nil || aws.ToString(got.TemplateSubject) != "Verify" {
		t.Fatalf("GetCustomVerificationEmailTemplate: %v %+v", err, got)
	}

	list, err := c.ListCustomVerificationEmailTemplates(ctx, &awsses.ListCustomVerificationEmailTemplatesInput{})
	if err != nil || len(list.CustomVerificationEmailTemplates) != 1 {
		t.Fatalf("ListCustomVerificationEmailTemplates = %d, %v", len(list.CustomVerificationEmailTemplates), err)
	}

	if _, err := c.DeleteCustomVerificationEmailTemplate(ctx, &awsses.DeleteCustomVerificationEmailTemplateInput{
		TemplateName: aws.String("verify"),
	}); err != nil {
		t.Fatalf("DeleteCustomVerificationEmailTemplate: %v", err)
	}
}

func TestSDKEventDestinations(t *testing.T) {
	ctx := context.Background()
	c := newSESClient(t)

	if _, err := c.CreateConfigurationSet(ctx, &awsses.CreateConfigurationSetInput{
		ConfigurationSetName: aws.String("cse"),
	}); err != nil {
		t.Fatalf("CreateConfigurationSet: %v", err)
	}

	if _, err := c.CreateConfigurationSetEventDestination(ctx, &awsses.CreateConfigurationSetEventDestinationInput{
		ConfigurationSetName: aws.String("cse"),
		EventDestinationName: aws.String("ed1"),
		EventDestination: &sestypes.EventDestinationDefinition{
			Enabled:            true,
			MatchingEventTypes: []sestypes.EventType{sestypes.EventTypeSend},
		},
	}); err != nil {
		t.Fatalf("CreateConfigurationSetEventDestination: %v", err)
	}

	got, err := c.GetConfigurationSetEventDestinations(ctx, &awsses.GetConfigurationSetEventDestinationsInput{
		ConfigurationSetName: aws.String("cse"),
	})
	if err != nil || len(got.EventDestinations) != 1 {
		t.Fatalf("GetConfigurationSetEventDestinations = %d, %v", len(got.EventDestinations), err)
	}

	if _, err := c.DeleteConfigurationSetEventDestination(ctx, &awsses.DeleteConfigurationSetEventDestinationInput{
		ConfigurationSetName: aws.String("cse"),
		EventDestinationName: aws.String("ed1"),
	}); err != nil {
		t.Fatalf("DeleteConfigurationSetEventDestination: %v", err)
	}
}

func TestSDKConfigSetPutOptions(t *testing.T) {
	ctx := context.Background()
	c := newSESClient(t)

	if _, err := c.CreateConfigurationSet(ctx, &awsses.CreateConfigurationSetInput{
		ConfigurationSetName: aws.String("cso"),
	}); err != nil {
		t.Fatalf("CreateConfigurationSet: %v", err)
	}

	if _, err := c.PutConfigurationSetSendingOptions(ctx, &awsses.PutConfigurationSetSendingOptionsInput{
		ConfigurationSetName: aws.String("cso"),
		SendingEnabled:       false,
	}); err != nil {
		t.Fatalf("PutConfigurationSetSendingOptions: %v", err)
	}

	got, err := c.GetConfigurationSet(ctx, &awsses.GetConfigurationSetInput{ConfigurationSetName: aws.String("cso")})
	if err != nil || got.SendingOptions == nil || got.SendingOptions.SendingEnabled {
		t.Fatalf("sending should be disabled: %v %+v", err, got.SendingOptions)
	}

	if _, err := c.PutConfigurationSetSuppressionOptions(ctx, &awsses.PutConfigurationSetSuppressionOptionsInput{
		ConfigurationSetName: aws.String("cso"),
		SuppressedReasons:    []sestypes.SuppressionListReason{sestypes.SuppressionListReasonBounce},
	}); err != nil {
		t.Fatalf("PutConfigurationSetSuppressionOptions: %v", err)
	}
}

func TestSDKDedicatedIpPoolLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newSESClient(t)

	if _, err := c.CreateDedicatedIpPool(ctx, &awsses.CreateDedicatedIpPoolInput{
		PoolName:    aws.String("pool1"),
		ScalingMode: sestypes.ScalingModeStandard,
	}); err != nil {
		t.Fatalf("CreateDedicatedIpPool: %v", err)
	}

	got, err := c.GetDedicatedIpPool(ctx, &awsses.GetDedicatedIpPoolInput{PoolName: aws.String("pool1")})
	if err != nil || got.DedicatedIpPool == nil {
		t.Fatalf("GetDedicatedIpPool: %v %+v", err, got)
	}

	if _, err := c.PutDedicatedIpInPool(ctx, &awsses.PutDedicatedIpInPoolInput{
		Ip:                  aws.String("192.0.2.1"),
		DestinationPoolName: aws.String("pool1"),
	}); err != nil {
		t.Fatalf("PutDedicatedIpInPool: %v", err)
	}

	ip, err := c.GetDedicatedIp(ctx, &awsses.GetDedicatedIpInput{Ip: aws.String("192.0.2.1")})
	if err != nil || ip.DedicatedIp == nil || aws.ToString(ip.DedicatedIp.PoolName) != "pool1" {
		t.Fatalf("GetDedicatedIp: %v %+v", err, ip)
	}

	list, err := c.ListDedicatedIpPools(ctx, &awsses.ListDedicatedIpPoolsInput{})
	if err != nil || len(list.DedicatedIpPools) != 1 {
		t.Fatalf("ListDedicatedIpPools = %d, %v", len(list.DedicatedIpPools), err)
	}

	if _, err := c.DeleteDedicatedIpPool(ctx, &awsses.DeleteDedicatedIpPoolInput{PoolName: aws.String("pool1")}); err != nil {
		t.Fatalf("DeleteDedicatedIpPool: %v", err)
	}
}

func TestSDKDeliverabilityDashboard(t *testing.T) {
	ctx := context.Background()
	c := newSESClient(t)

	if _, err := c.PutDeliverabilityDashboardOption(ctx, &awsses.PutDeliverabilityDashboardOptionInput{
		DashboardEnabled: true,
	}); err != nil {
		t.Fatalf("PutDeliverabilityDashboardOption: %v", err)
	}

	opts, err := c.GetDeliverabilityDashboardOptions(ctx, &awsses.GetDeliverabilityDashboardOptionsInput{})
	if err != nil || !opts.DashboardEnabled {
		t.Fatalf("GetDeliverabilityDashboardOptions: %v %+v", err, opts)
	}

	rep, err := c.CreateDeliverabilityTestReport(ctx, &awsses.CreateDeliverabilityTestReportInput{
		FromEmailAddress: aws.String("from@example.com"),
		Content: &sestypes.EmailContent{Simple: &sestypes.Message{
			Subject: &sestypes.Content{Data: aws.String("Hi")},
			Body:    &sestypes.Body{Text: &sestypes.Content{Data: aws.String("Body")}},
		}},
	})
	if err != nil || aws.ToString(rep.ReportId) == "" {
		t.Fatalf("CreateDeliverabilityTestReport: %v %+v", err, rep)
	}

	if _, err := c.GetDeliverabilityTestReport(ctx, &awsses.GetDeliverabilityTestReportInput{
		ReportId: rep.ReportId,
	}); err != nil {
		t.Fatalf("GetDeliverabilityTestReport: %v", err)
	}
}

func TestSDKImportExportJobs(t *testing.T) {
	ctx := context.Background()
	c := newSESClient(t)

	exp, err := c.CreateExportJob(ctx, &awsses.CreateExportJobInput{
		ExportDataSource: &sestypes.ExportDataSource{},
		ExportDestination: &sestypes.ExportDestination{
			DataFormat: sestypes.DataFormatCsv,
		},
	})
	if err != nil || aws.ToString(exp.JobId) == "" {
		t.Fatalf("CreateExportJob: %v %+v", err, exp)
	}

	got, err := c.GetExportJob(ctx, &awsses.GetExportJobInput{JobId: exp.JobId})
	if err != nil || aws.ToString(got.JobId) == "" {
		t.Fatalf("GetExportJob: %v %+v", err, got)
	}

	list, err := c.ListExportJobs(ctx, &awsses.ListExportJobsInput{})
	if err != nil || len(list.ExportJobs) != 1 {
		t.Fatalf("ListExportJobs = %d, %v", len(list.ExportJobs), err)
	}
}

func TestSDKTenantLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newSESClient(t)

	created, err := c.CreateTenant(ctx, &awsses.CreateTenantInput{TenantName: aws.String("t1")})
	if err != nil || aws.ToString(created.TenantName) != "t1" {
		t.Fatalf("CreateTenant: %v %+v", err, created)
	}

	got, err := c.GetTenant(ctx, &awsses.GetTenantInput{TenantName: aws.String("t1")})
	if err != nil || got.Tenant == nil || aws.ToString(got.Tenant.TenantName) != "t1" {
		t.Fatalf("GetTenant: %v %+v", err, got)
	}

	list, err := c.ListTenants(ctx, &awsses.ListTenantsInput{})
	if err != nil || len(list.Tenants) != 1 {
		t.Fatalf("ListTenants = %d, %v", len(list.Tenants), err)
	}

	if _, err := c.DeleteTenant(ctx, &awsses.DeleteTenantInput{TenantName: aws.String("t1")}); err != nil {
		t.Fatalf("DeleteTenant: %v", err)
	}
}

func TestSDKMultiRegionEndpointLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newSESClient(t)

	created, err := c.CreateMultiRegionEndpoint(ctx, &awsses.CreateMultiRegionEndpointInput{
		EndpointName: aws.String("ep1"),
		Details: &sestypes.Details{
			RoutesDetails: []sestypes.RouteDetails{{Region: aws.String("us-west-2")}},
		},
	})
	if err != nil || aws.ToString(created.EndpointId) == "" {
		t.Fatalf("CreateMultiRegionEndpoint: %v %+v", err, created)
	}

	got, err := c.GetMultiRegionEndpoint(ctx, &awsses.GetMultiRegionEndpointInput{EndpointName: aws.String("ep1")})
	if err != nil || aws.ToString(got.EndpointId) == "" {
		t.Fatalf("GetMultiRegionEndpoint: %v %+v", err, got)
	}

	list, err := c.ListMultiRegionEndpoints(ctx, &awsses.ListMultiRegionEndpointsInput{})
	if err != nil || len(list.MultiRegionEndpoints) != 1 {
		t.Fatalf("ListMultiRegionEndpoints = %d, %v", len(list.MultiRegionEndpoints), err)
	}

	if _, err := c.DeleteMultiRegionEndpoint(ctx, &awsses.DeleteMultiRegionEndpointInput{
		EndpointName: aws.String("ep1"),
	}); err != nil {
		t.Fatalf("DeleteMultiRegionEndpoint: %v", err)
	}
}

func TestSDKSendBulkEmail(t *testing.T) {
	ctx := context.Background()
	c := newSESClient(t)

	if _, err := c.CreateEmailIdentity(ctx, &awsses.CreateEmailIdentityInput{
		EmailIdentity: aws.String("sender@example.com"),
	}); err != nil {
		t.Fatalf("create identity: %v", err)
	}

	if _, err := c.CreateEmailTemplate(ctx, &awsses.CreateEmailTemplateInput{
		TemplateName:    aws.String("bulk"),
		TemplateContent: &sestypes.EmailTemplateContent{Subject: aws.String("Hi"), Text: aws.String("Body")},
	}); err != nil {
		t.Fatalf("create template: %v", err)
	}

	out, err := c.SendBulkEmail(ctx, &awsses.SendBulkEmailInput{
		FromEmailAddress: aws.String("sender@example.com"),
		DefaultContent: &sestypes.BulkEmailContent{
			Template: &sestypes.Template{TemplateName: aws.String("bulk")},
		},
		BulkEmailEntries: []sestypes.BulkEmailEntry{
			{Destination: &sestypes.Destination{ToAddresses: []string{"a@x.com"}}},
			{Destination: &sestypes.Destination{ToAddresses: []string{"b@x.com"}}},
		},
	})
	if err != nil || len(out.BulkEmailEntryResults) != 2 {
		t.Fatalf("SendBulkEmail = %d, %v", len(out.BulkEmailEntryResults), err)
	}
}

func TestSDKReputationEntity(t *testing.T) {
	ctx := context.Background()
	c := newSESClient(t)

	got, err := c.GetReputationEntity(ctx, &awsses.GetReputationEntityInput{
		ReputationEntityType:      sestypes.ReputationEntityTypeResource,
		ReputationEntityReference: aws.String("arn:aws:ses:us-east-1:123456789012:identity/x"),
	})
	if err != nil || got.ReputationEntity == nil {
		t.Fatalf("GetReputationEntity: %v %+v", err, got)
	}

	list, err := c.ListReputationEntities(ctx, &awsses.ListReputationEntitiesInput{})
	if err != nil || len(list.ReputationEntities) != 1 {
		t.Fatalf("ListReputationEntities = %d, %v", len(list.ReputationEntities), err)
	}
}

func TestSDKIdentityPolicy(t *testing.T) {
	ctx := context.Background()
	c := newSESClient(t)

	if _, err := c.CreateEmailIdentity(ctx, &awsses.CreateEmailIdentityInput{
		EmailIdentity: aws.String("pol@example.com"),
	}); err != nil {
		t.Fatalf("create identity: %v", err)
	}

	if _, err := c.CreateEmailIdentityPolicy(ctx, &awsses.CreateEmailIdentityPolicyInput{
		EmailIdentity: aws.String("pol@example.com"),
		PolicyName:    aws.String("p1"),
		Policy:        aws.String(`{"Version":"2012-10-17"}`),
	}); err != nil {
		t.Fatalf("CreateEmailIdentityPolicy: %v", err)
	}

	got, err := c.GetEmailIdentityPolicies(ctx, &awsses.GetEmailIdentityPoliciesInput{
		EmailIdentity: aws.String("pol@example.com"),
	})
	if err != nil || len(got.Policies) != 1 {
		t.Fatalf("GetEmailIdentityPolicies = %d, %v", len(got.Policies), err)
	}

	if _, err := c.DeleteEmailIdentityPolicy(ctx, &awsses.DeleteEmailIdentityPolicyInput{
		EmailIdentity: aws.String("pol@example.com"),
		PolicyName:    aws.String("p1"),
	}); err != nil {
		t.Fatalf("DeleteEmailIdentityPolicy: %v", err)
	}
}
