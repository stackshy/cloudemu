package route53_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsr53 "github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
)

// TestSDKZoneCommentPersisted locks that HostedZoneConfig.Comment is persisted
// and returned on both Create and Get — a perpetual-diff risk for Terraform's
// aws_route53_zone, which reads the comment back after setting it.
func TestSDKZoneCommentPersisted(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	created, err := client.CreateHostedZone(ctx, &awsr53.CreateHostedZoneInput{
		Name:            aws.String("commented.com."),
		CallerReference: aws.String("comment-ref"),
		HostedZoneConfig: &r53types.HostedZoneConfig{
			Comment: aws.String("my important zone"),
		},
	})
	if err != nil {
		t.Fatalf("CreateHostedZone: %v", err)
	}

	if created.HostedZone.Config == nil ||
		aws.ToString(created.HostedZone.Config.Comment) != "my important zone" {
		t.Fatalf("CreateHostedZone comment = %+v, want \"my important zone\"", created.HostedZone.Config)
	}

	zoneID := aws.ToString(created.HostedZone.Id)
	got, err := client.GetHostedZone(ctx, &awsr53.GetHostedZoneInput{Id: aws.String(zoneID)})
	if err != nil {
		t.Fatalf("GetHostedZone: %v", err)
	}

	if got.HostedZone.Config == nil ||
		aws.ToString(got.HostedZone.Config.Comment) != "my important zone" {
		t.Fatalf("GetHostedZone comment = %+v, want \"my important zone\"", got.HostedZone.Config)
	}
}
