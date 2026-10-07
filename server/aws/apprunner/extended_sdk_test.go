package apprunner_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsar "github.com/aws/aws-sdk-go-v2/service/apprunner"
	artypes "github.com/aws/aws-sdk-go-v2/service/apprunner/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func createPrivateService(t *testing.T, c *awsar.Client, url, name string) *artypes.Service {
	t.Helper()

	body := `{"ServiceName":"` + name + `","SourceConfiguration":{"ImageRepository":{"ImageIdentifier":"public.ecr.aws/x/app:1",` +
		`"ImageRepositoryType":"ECR_PUBLIC","ImageConfiguration":{"Port":"8080"}},"AutoDeploymentsEnabled":false},` +
		`"NetworkConfiguration":{"IngressConfiguration":{"IsPubliclyAccessible":false}}}`

	if code, resp := rawCall(t, url, "CreateService", body); code != http.StatusOK {
		t.Fatalf("CreateService(%s): %d %s", name, code, resp)
	}

	list, err := c.ListServices(context.Background(), &awsar.ListServicesInput{})
	if err != nil {
		t.Fatalf("ListServices: %v", err)
	}

	for i := range list.ServiceSummaryList {
		if aws.ToString(list.ServiceSummaryList[i].ServiceName) == name {
			d, derr := c.DescribeService(context.Background(), &awsar.DescribeServiceInput{ServiceArn: list.ServiceSummaryList[i].ServiceArn})
			if derr != nil {
				t.Fatalf("DescribeService: %v", derr)
			}

			return d.Service
		}
	}

	t.Fatalf("service %s not listed", name)

	return nil
}

func TestSDKVpcIngressLifecycle(t *testing.T) {
	ctx := context.Background()
	c, url := newClientURL(t)
	priv := createPrivateService(t, c, url, "priv-app")
	pub := mustCreate(t, c)

	cfg := &artypes.IngressVpcConfiguration{VpcId: aws.String("vpc-0abc"), VpcEndpointId: aws.String("vpce-0abc")}

	if _, err := c.CreateVpcIngressConnection(ctx, &awsar.CreateVpcIngressConnectionInput{
		VpcIngressConnectionName: aws.String("ing-pub"), ServiceArn: pub.ServiceArn, IngressVpcConfiguration: cfg,
	}); err == nil {
		t.Fatal("a public service must be rejected")
	}

	out, err := c.CreateVpcIngressConnection(ctx, &awsar.CreateVpcIngressConnectionInput{
		VpcIngressConnectionName: aws.String("ing-one"), ServiceArn: priv.ServiceArn, IngressVpcConfiguration: cfg,
	})
	if err != nil {
		t.Fatalf("CreateVpcIngressConnection: %v", err)
	}

	conn := out.VpcIngressConnection
	if conn.Status != artypes.VpcIngressConnectionStatusAvailable || aws.ToString(conn.DomainName) == "" {
		t.Fatalf("unexpected connection: %+v", conn)
	}

	if _, err = c.CreateVpcIngressConnection(ctx, &awsar.CreateVpcIngressConnectionInput{
		VpcIngressConnectionName: aws.String("ing-one"), ServiceArn: priv.ServiceArn, IngressVpcConfiguration: cfg,
	}); err == nil {
		t.Fatal("duplicate name must be rejected")
	}

	upd, err := c.UpdateVpcIngressConnection(ctx, &awsar.UpdateVpcIngressConnectionInput{
		VpcIngressConnectionArn: conn.VpcIngressConnectionArn,
		IngressVpcConfiguration: &artypes.IngressVpcConfiguration{VpcId: aws.String("vpc-0def"), VpcEndpointId: aws.String("vpce-0def")},
	})
	if err != nil || aws.ToString(upd.VpcIngressConnection.IngressVpcConfiguration.VpcEndpointId) != "vpce-0def" {
		t.Fatalf("UpdateVpcIngressConnection: %v %+v", err, upd)
	}

	list, err := c.ListVpcIngressConnections(ctx, &awsar.ListVpcIngressConnectionsInput{
		Filter: &artypes.ListVpcIngressConnectionsFilter{ServiceArn: priv.ServiceArn},
	})
	if err != nil || len(list.VpcIngressConnectionSummaryList) != 1 {
		t.Fatalf("ListVpcIngressConnections: %v %+v", err, list)
	}

	if _, err = c.DeleteVpcIngressConnection(ctx, &awsar.DeleteVpcIngressConnectionInput{VpcIngressConnectionArn: conn.VpcIngressConnectionArn}); err != nil {
		t.Fatalf("DeleteVpcIngressConnection: %v", err)
	}

	_, err = c.DescribeVpcIngressConnection(ctx, &awsar.DescribeVpcIngressConnectionInput{VpcIngressConnectionArn: conn.VpcIngressConnectionArn})

	var nf *artypes.ResourceNotFoundException
	if !errors.As(err, &nf) {
		t.Fatalf("describe after delete: want ResourceNotFoundException, got %v", err)
	}
}

func TestSDKCustomDomains(t *testing.T) {
	ctx := context.Background()
	c, url := newClientURL(t)
	svc := mustCreate(t, c)

	assoc, err := c.AssociateCustomDomain(ctx, &awsar.AssociateCustomDomainInput{
		ServiceArn: svc.ServiceArn, DomainName: aws.String("app.example.com"),
	})
	if err != nil {
		t.Fatalf("AssociateCustomDomain: %v", err)
	}

	if assoc.CustomDomain.Status != artypes.CustomDomainAssociationStatusPendingCertificateDnsValidation ||
		len(assoc.CustomDomain.CertificateValidationRecords) == 0 || aws.ToString(assoc.DNSTarget) == "" {
		t.Fatalf("unexpected association: %+v", assoc)
	}

	if _, err = c.AssociateCustomDomain(ctx, &awsar.AssociateCustomDomainInput{
		ServiceArn: svc.ServiceArn, DomainName: aws.String("app.example.com"),
	}); err == nil {
		t.Fatal("the same domain twice must be rejected")
	}

	desc, err := c.DescribeCustomDomains(ctx, &awsar.DescribeCustomDomainsInput{ServiceArn: svc.ServiceArn})
	if err != nil || len(desc.CustomDomains) != 1 {
		t.Fatalf("DescribeCustomDomains: %v %+v", err, desc)
	}

	if _, err = c.DisassociateCustomDomain(ctx, &awsar.DisassociateCustomDomainInput{
		ServiceArn: svc.ServiceArn, DomainName: aws.String("app.example.com"),
	}); err != nil {
		t.Fatalf("DisassociateCustomDomain: %v", err)
	}

	desc, err = c.DescribeCustomDomains(ctx, &awsar.DescribeCustomDomainsInput{ServiceArn: svc.ServiceArn})
	if err != nil || len(desc.CustomDomains) != 0 {
		t.Fatalf("domains after disassociate: %v %+v", err, desc)
	}

	// The empty list must be on the wire as [] (a missing member breaks CLI queries).
	code, body := rawCall(t, url, "DescribeCustomDomains", `{"ServiceArn":"`+aws.ToString(svc.ServiceArn)+`"}`)
	if code != http.StatusOK || !strings.Contains(body, `"CustomDomains":[]`) {
		t.Fatalf("empty CustomDomains must serialize as []: %d %s", code, body)
	}
}

func TestSDKUpdateDefaultAndListServicesFor(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	cfg, err := c.CreateAutoScalingConfiguration(ctx, &awsar.CreateAutoScalingConfigurationInput{
		AutoScalingConfigurationName: aws.String("custom-as"), MaxSize: aws.Int32(5),
	})
	if err != nil {
		t.Fatalf("CreateAutoScalingConfiguration: %v", err)
	}

	arn := cfg.AutoScalingConfiguration.AutoScalingConfigurationArn

	if _, err = c.UpdateDefaultAutoScalingConfiguration(ctx, &awsar.UpdateDefaultAutoScalingConfigurationInput{AutoScalingConfigurationArn: arn}); err != nil {
		t.Fatalf("UpdateDefaultAutoScalingConfiguration: %v", err)
	}

	// A service created without an explicit configuration now uses the new default.
	svc := mustCreate(t, c)
	if aws.ToString(svc.AutoScalingConfigurationSummary.AutoScalingConfigurationArn) != aws.ToString(arn) {
		t.Fatalf("service did not pick the new default: %+v", svc.AutoScalingConfigurationSummary)
	}

	list, err := c.ListServicesForAutoScalingConfiguration(ctx, &awsar.ListServicesForAutoScalingConfigurationInput{AutoScalingConfigurationArn: arn})
	if err != nil || len(list.ServiceArnList) != 1 || list.ServiceArnList[0] != aws.ToString(svc.ServiceArn) {
		t.Fatalf("ListServicesForAutoScalingConfiguration: %v %+v", err, list)
	}
}

func TestSDKLatestOnlyDefaultsToTrue(t *testing.T) {
	ctx := context.Background()
	c, url := newClientURL(t)

	for i := 0; i < 2; i++ {
		if _, err := c.CreateAutoScalingConfiguration(ctx, &awsar.CreateAutoScalingConfigurationInput{AutoScalingConfigurationName: aws.String("rev-cfg")}); err != nil {
			t.Fatalf("create revision %d: %v", i, err)
		}
	}

	latest, err := c.ListAutoScalingConfigurations(ctx, &awsar.ListAutoScalingConfigurationsInput{AutoScalingConfigurationName: aws.String("rev-cfg")})
	if err != nil || len(latest.AutoScalingConfigurationSummaryList) != 1 {
		t.Fatalf("default LatestOnly must list one revision: %v %+v", err, latest)
	}

	code, body := rawCall(t, url, "ListAutoScalingConfigurations", `{"AutoScalingConfigurationName":"rev-cfg","LatestOnly":false}`)
	if code != http.StatusOK || strings.Count(body, `"AutoScalingConfigurationRevision"`) != 2 {
		t.Fatalf("LatestOnly=false must list both revisions: %d %s", code, body)
	}
}

func TestPhantomRevisionOperationsAreUnknown(t *testing.T) {
	ts := newRawServer(t)

	for _, op := range []string{"ListAutoScalingConfigurationRevisions", "ListObservabilityConfigurationRevisions"} {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL, bytes.NewBufferString("{}"))
		req.Header.Set("X-Amz-Target", "AppRunner."+op)
		req.Header.Set("Content-Type", "application/x-amz-json-1.0")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", op, err)
		}

		_ = resp.Body.Close()

		if resp.StatusCode == http.StatusOK || !strings.Contains(resp.Header.Get("X-Amzn-Errortype"), "UnknownOperation") {
			t.Fatalf("%s: status %d type %q, want UnknownOperationException", op, resp.StatusCode, resp.Header.Get("X-Amzn-Errortype"))
		}
	}
}

func newRawServer(t *testing.T) *httptest.Server {
	t.Helper()

	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{AppRunner: cloudemu.NewAWS().AppRunner}))
	t.Cleanup(ts.Close)

	return ts
}

// rawCall posts one AppRunner JSON 1.0 operation. The Go SDK drops a false
// non-pointer bool from the request, so false-valued members (private ingress,
// LatestOnly=false) can only reach the server this way.
func rawCall(t *testing.T, url, op, body string) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	req.Header.Set("X-Amz-Target", "AppRunner."+op)
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s: %v", op, err)
	}

	defer resp.Body.Close()

	var buf bytes.Buffer

	_, _ = buf.ReadFrom(resp.Body)

	return resp.StatusCode, buf.String()
}

// newClientURL returns an SDK client plus the endpoint it talks to.
func newClientURL(t *testing.T) (*awsar.Client, string) {
	t.Helper()

	ts := newRawServer(t)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awsar.NewFromConfig(cfg, func(o *awsar.Options) { o.BaseEndpoint = aws.String(ts.URL) }), ts.URL
}
