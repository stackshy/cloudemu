package servicequotas_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	sq "github.com/aws/aws-sdk-go-v2/service/servicequotas"

	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	"github.com/stackshy/cloudemu/v2/features/quota"
)

// newServiceQuotasClient stands up an in-process AWS server backed by the seeded
// AWS default quota registry and returns a real Service Quotas SDK client.
func newServiceQuotasClient(t *testing.T) *sq.Client {
	t.Helper()

	srv := awsserver.New(awsserver.Drivers{
		ServiceQuotas: quota.NewAWSDefaults(nil),
		AccountID:     "000000000000",
		Region:        "us-east-1",
	})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return sq.NewFromConfig(cfg, func(o *sq.Options) { o.BaseEndpoint = aws.String(ts.URL) })
}

func TestSDKListServiceQuotas(t *testing.T) {
	ctx := context.Background()
	c := newServiceQuotasClient(t)

	out, err := c.ListServiceQuotas(ctx, &sq.ListServiceQuotasInput{ServiceCode: aws.String("vpc")})
	if err != nil {
		t.Fatalf("ListServiceQuotas: %v", err)
	}

	// The seeded VPC set has three quotas (VPCs, Subnets, security groups).
	if len(out.Quotas) != 3 {
		t.Fatalf("ListServiceQuotas returned %d quotas, want 3", len(out.Quotas))
	}

	var foundVPCs bool

	for _, q := range out.Quotas {
		if aws.ToString(q.QuotaCode) == "L-F678F1CE" {
			foundVPCs = true

			if aws.ToFloat64(q.Value) != 5 {
				t.Fatalf("VPCs per Region value = %v, want 5", aws.ToFloat64(q.Value))
			}

			if aws.ToString(q.QuotaArn) == "" {
				t.Fatal("VPCs per Region quota has empty QuotaArn")
			}
		}
	}

	if !foundVPCs {
		t.Fatalf("VPCs per Region quota (L-F678F1CE) missing from %+v", out.Quotas)
	}
}

func TestSDKGetServiceQuota(t *testing.T) {
	ctx := context.Background()
	c := newServiceQuotasClient(t)

	out, err := c.GetServiceQuota(ctx, &sq.GetServiceQuotaInput{
		ServiceCode: aws.String("lambda"),
		QuotaCode:   aws.String("L-B99A9384"),
	})
	if err != nil {
		t.Fatalf("GetServiceQuota: %v", err)
	}

	if aws.ToString(out.Quota.QuotaName) != "Concurrent executions" {
		t.Fatalf("QuotaName = %q, want Concurrent executions", aws.ToString(out.Quota.QuotaName))
	}

	if aws.ToFloat64(out.Quota.Value) != 1000 {
		t.Fatalf("Value = %v, want 1000", aws.ToFloat64(out.Quota.Value))
	}

	if !out.Quota.Adjustable {
		t.Fatal("Lambda concurrent executions should be Adjustable")
	}
}

func TestSDKGetAWSDefaultServiceQuota(t *testing.T) {
	ctx := context.Background()
	c := newServiceQuotasClient(t)

	out, err := c.GetAWSDefaultServiceQuota(ctx, &sq.GetAWSDefaultServiceQuotaInput{
		ServiceCode: aws.String("s3"),
		QuotaCode:   aws.String("L-DC2B2D3D"),
	})
	if err != nil {
		t.Fatalf("GetAWSDefaultServiceQuota: %v", err)
	}

	if aws.ToFloat64(out.Quota.Value) != 100 {
		t.Fatalf("default S3 buckets value = %v, want 100", aws.ToFloat64(out.Quota.Value))
	}
}

func TestSDKRequestIncreaseAndHistory(t *testing.T) {
	ctx := context.Background()
	c := newServiceQuotasClient(t)

	reqOut, err := c.RequestServiceQuotaIncrease(ctx, &sq.RequestServiceQuotaIncreaseInput{
		ServiceCode:  aws.String("ec2"),
		QuotaCode:    aws.String("L-1216C47A"),
		DesiredValue: aws.Float64(64),
	})
	if err != nil {
		t.Fatalf("RequestServiceQuotaIncrease: %v", err)
	}

	if aws.ToFloat64(reqOut.RequestedQuota.DesiredValue) != 64 {
		t.Fatalf("DesiredValue = %v, want 64", aws.ToFloat64(reqOut.RequestedQuota.DesiredValue))
	}

	if aws.ToString(reqOut.RequestedQuota.Id) == "" {
		t.Fatal("requested quota has empty Id")
	}

	if reqOut.RequestedQuota.Created == nil {
		t.Fatal("requested quota has nil Created timestamp")
	}

	histOut, err := c.ListRequestedServiceQuotaChangeHistory(ctx,
		&sq.ListRequestedServiceQuotaChangeHistoryInput{ServiceCode: aws.String("ec2")})
	if err != nil {
		t.Fatalf("ListRequestedServiceQuotaChangeHistory: %v", err)
	}

	if len(histOut.RequestedQuotas) != 1 {
		t.Fatalf("history has %d entries, want 1", len(histOut.RequestedQuotas))
	}

	if aws.ToString(histOut.RequestedQuotas[0].Id) != aws.ToString(reqOut.RequestedQuota.Id) {
		t.Fatalf("history entry Id = %q, want %q",
			aws.ToString(histOut.RequestedQuotas[0].Id), aws.ToString(reqOut.RequestedQuota.Id))
	}
}
