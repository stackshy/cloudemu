package main

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
)

func route53Client(t *testing.T, endpoint string, c aws.Credentials) *route53.Client {
	t.Helper()

	return route53.NewFromConfig(credsConfig(t, c), func(o *route53.Options) { o.BaseEndpoint = aws.String(endpoint) })
}

func cloudFrontClient(t *testing.T, endpoint string, c aws.Credentials) *cloudfront.Client {
	t.Helper()

	return cloudfront.NewFromConfig(credsConfig(t, c), func(o *cloudfront.Options) { o.BaseEndpoint = aws.String(endpoint) })
}

func txtChange(name string, rtype r53types.RRType, value string) *r53types.ChangeBatch {
	return &r53types.ChangeBatch{Changes: []r53types.Change{{
		Action: r53types.ChangeActionCreate,
		ResourceRecordSet: &r53types.ResourceRecordSet{
			Name: aws.String(name), Type: rtype, TTL: aws.Int64(60),
			ResourceRecords: []r53types.ResourceRecord{{Value: aws.String(value)}},
		},
	}}}
}

// TestEnforceAuthRoute53 drives the real Route 53 SDK against cloudemu serve
// with --enforce-auth: record changes are authorized on the hosted zone ARN
// and limited by the record type condition key.
func TestEnforceAuthRoute53(t *testing.T) {
	endpoint, stop := enforceAuthServer(t)
	defer stop()

	ctx := context.Background()
	boot := clientsFor(t, endpoint, seedBootUser(t, endpoint))
	admin := route53Client(t, endpoint, boot.cred)

	zone, err := admin.CreateHostedZone(ctx, &route53.CreateHostedZoneInput{
		Name: aws.String("authz.example.com"), CallerReference: aws.String("authz"),
	})
	wantOK(t, "CreateHostedZone as boot", err)

	other, err := admin.CreateHostedZone(ctx, &route53.CreateHostedZoneInput{
		Name: aws.String("other.example.com"), CallerReference: aws.String("other"),
	})
	wantOK(t, "CreateHostedZone other as boot", err)

	zoneARN := "arn:aws:route53:::hostedzone/" + strings.TrimPrefix(aws.ToString(zone.HostedZone.Id), "/hostedzone/")
	txt := route53Client(t, endpoint, boot.newUser(t, "txtwriter", `{"Version":"2012-10-17","Statement":[`+
		`{"Effect":"Allow","Action":"route53:ChangeResourceRecordSets","Resource":"`+zoneARN+`",`+
		`"Condition":{"ForAllValues:StringEquals":{"route53:ChangeResourceRecordSetsRecordTypes":["TXT"]}}},`+
		`{"Effect":"Allow","Action":["route53:GetHostedZone","route53:ListResourceRecordSets"],"Resource":"`+zoneARN+`"}]}`))

	_, err = txt.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: zone.HostedZone.Id, ChangeBatch: txtChange("v.authz.example.com", r53types.RRTypeTxt, `"v=1"`),
	})
	wantOK(t, "TXT change", err)

	_, err = txt.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: zone.HostedZone.Id, ChangeBatch: txtChange("a.authz.example.com", r53types.RRTypeA, "192.0.2.1"),
	})
	wantCode(t, "A change", err, "AccessDenied")

	_, err = txt.GetHostedZone(ctx, &route53.GetHostedZoneInput{Id: zone.HostedZone.Id})
	wantOK(t, "GetHostedZone", err)

	_, err = txt.GetHostedZone(ctx, &route53.GetHostedZoneInput{Id: other.HostedZone.Id})
	wantCode(t, "GetHostedZone of another zone", err, "AccessDenied")

	_, err = txt.DeleteHostedZone(ctx, &route53.DeleteHostedZoneInput{Id: zone.HostedZone.Id})
	wantCode(t, "DeleteHostedZone", err, "AccessDenied")

	recs, err := txt.ListResourceRecordSets(ctx, &route53.ListResourceRecordSetsInput{HostedZoneId: zone.HostedZone.Id})
	wantOK(t, "ListResourceRecordSets", err)

	for _, rr := range recs.ResourceRecordSets {
		if rr.Type == r53types.RRTypeA {
			t.Fatalf("a denied change created %s", aws.ToString(rr.Name))
		}
	}
}

// TestEnforceAuthCloudFront drives the real CloudFront SDK against cloudemu
// serve with --enforce-auth: distribution operations are authorized on the
// distribution ARN, and CreateDistributionWithTags also needs TagResource.
func TestEnforceAuthCloudFront(t *testing.T) {
	endpoint, stop := enforceAuthServer(t)
	defer stop()

	ctx := context.Background()
	boot := clientsFor(t, endpoint, seedBootUser(t, endpoint))
	admin := cloudFrontClient(t, endpoint, boot.cred)

	cfg := func(ref string) *cftypes.DistributionConfig {
		return &cftypes.DistributionConfig{
			CallerReference: aws.String(ref), Comment: aws.String(""), Enabled: aws.Bool(true),
			Origins: &cftypes.Origins{Quantity: aws.Int32(1), Items: []cftypes.Origin{{
				Id: aws.String("o1"), DomainName: aws.String("example.com"),
				CustomOriginConfig: &cftypes.CustomOriginConfig{
					HTTPPort: aws.Int32(80), HTTPSPort: aws.Int32(443), OriginProtocolPolicy: cftypes.OriginProtocolPolicyHttpsOnly,
				},
			}}},
			DefaultCacheBehavior: &cftypes.DefaultCacheBehavior{
				TargetOriginId: aws.String("o1"), ViewerProtocolPolicy: cftypes.ViewerProtocolPolicyAllowAll,
				CachePolicyId: aws.String("658327ea-f89d-4fab-a63d-7e88639e58f6"),
			},
		}
	}

	dist, err := admin.CreateDistribution(ctx, &cloudfront.CreateDistributionInput{DistributionConfig: cfg("one")})
	wantOK(t, "CreateDistribution as boot", err)

	arn := aws.ToString(dist.Distribution.ARN)
	operator := cloudFrontClient(t, endpoint, boot.newUser(t, "cfoperator", `{"Version":"2012-10-17","Statement":[`+
		`{"Effect":"Allow","Action":["cloudfront:GetDistribution","cloudfront:CreateInvalidation"],"Resource":"`+arn+`"},`+
		`{"Effect":"Allow","Action":"cloudfront:CreateDistribution","Resource":"*"}]}`))

	_, err = operator.GetDistribution(ctx, &cloudfront.GetDistributionInput{Id: dist.Distribution.Id})
	wantOK(t, "GetDistribution", err)

	_, err = operator.CreateInvalidation(ctx, &cloudfront.CreateInvalidationInput{
		DistributionId: dist.Distribution.Id,
		InvalidationBatch: &cftypes.InvalidationBatch{
			CallerReference: aws.String("inv"), Paths: &cftypes.Paths{Quantity: aws.Int32(1), Items: []string{"/*"}},
		},
	})
	wantOK(t, "CreateInvalidation", err)

	_, err = operator.DeleteDistribution(ctx, &cloudfront.DeleteDistributionInput{Id: dist.Distribution.Id, IfMatch: dist.ETag})
	wantCode(t, "DeleteDistribution", err, "AccessDenied")

	if !strings.Contains(err.Error(), "cloudfront:DeleteDistribution on resource: "+arn) {
		t.Fatalf("deny message = %v", err)
	}

	_, err = operator.CreateDistributionWithTags(ctx, &cloudfront.CreateDistributionWithTagsInput{
		DistributionConfigWithTags: &cftypes.DistributionConfigWithTags{
			DistributionConfig: cfg("tagged"),
			Tags:               &cftypes.Tags{Items: []cftypes.Tag{{Key: aws.String("team"), Value: aws.String("a")}}},
		},
	})
	wantCode(t, "CreateDistributionWithTags without TagResource", err, "AccessDenied")

	_, err = operator.CreateDistribution(ctx, &cloudfront.CreateDistributionInput{DistributionConfig: cfg("plain")})
	wantOK(t, "CreateDistribution", err)
}
