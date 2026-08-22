package aws

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsr53 "github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestAWSDNSCompat drives a Route 53 hosted-zone and record lifecycle through
// the real aws-sdk-go-v2 client against CloudEmu's in-process wire server.
// Operation names match the portable "dns" driver in
// docs/coverage/coverage.json: Route 53 manages records through the batch
// ChangeResourceRecordSets call, so a CREATE change maps to "CreateRecord", an
// UPSERT to "UpdateRecord" and a DELETE to "DeleteRecord"; ListResourceRecordSets
// maps to "ListRecords"; and ChangeTagsForResource (the only mutable zone field)
// maps to "UpdateZone".
func TestAWSDNSCompat(t *testing.T) {
	provider := cloudemu.NewAWS()
	sess := compat.BootAWS(t, awsserver.Drivers{Route53: provider.Route53})

	client := awsr53.NewFromConfig(sess.Config(), func(o *awsr53.Options) {
		o.BaseEndpoint = aws.String(sess.Endpoint())
	})
	ctx := context.Background()

	const (
		svc      = "dns"
		zoneName = "example.com."
		recName  = "www.example.com."
		ttl      = 300
	)

	var zoneID string

	sess.Op(svc, "CreateZone", func() error {
		out, err := client.CreateHostedZone(ctx, &awsr53.CreateHostedZoneInput{
			Name:            aws.String(zoneName),
			CallerReference: aws.String("compat-ref-1"),
			HostedZoneConfig: &r53types.HostedZoneConfig{
				Comment: aws.String("compat zone"),
			},
		})
		if err != nil {
			return err
		}

		zoneID = aws.ToString(out.HostedZone.Id)
		if zoneID == "" || aws.ToString(out.HostedZone.Name) != zoneName {
			return fmt.Errorf("CreateHostedZone = %+v", out.HostedZone)
		}

		return nil
	})

	sess.Op(svc, "GetZone", func() error {
		out, err := client.GetHostedZone(ctx, &awsr53.GetHostedZoneInput{Id: aws.String(zoneID)})
		if err != nil {
			return err
		}

		if aws.ToString(out.HostedZone.Name) != zoneName {
			return fmt.Errorf("GetHostedZone name = %q, want %q", aws.ToString(out.HostedZone.Name), zoneName)
		}

		return nil
	})

	sess.Op(svc, "ListZones", func() error {
		out, err := client.ListHostedZones(ctx, &awsr53.ListHostedZonesInput{})
		if err != nil {
			return err
		}

		for _, z := range out.HostedZones {
			if aws.ToString(z.Name) == zoneName {
				return nil
			}
		}

		return fmt.Errorf("zone %q not found in ListHostedZones", zoneName)
	})

	sess.Op(svc, "UpdateZone", func() error {
		_, err := client.ChangeTagsForResource(ctx, &awsr53.ChangeTagsForResourceInput{
			ResourceType: r53types.TagResourceTypeHostedzone,
			ResourceId:   aws.String(strings.TrimPrefix(zoneID, "/hostedzone/")),
			AddTags:      []r53types.Tag{{Key: aws.String("env"), Value: aws.String("compat")}},
		})

		return err
	})

	sess.Op(svc, "CreateRecord", func() error {
		_, err := client.ChangeResourceRecordSets(ctx, &awsr53.ChangeResourceRecordSetsInput{
			HostedZoneId: aws.String(zoneID),
			ChangeBatch: &r53types.ChangeBatch{
				Changes: []r53types.Change{{
					Action: r53types.ChangeActionCreate,
					ResourceRecordSet: &r53types.ResourceRecordSet{
						Name:            aws.String(recName),
						Type:            r53types.RRTypeA,
						TTL:             aws.Int64(ttl),
						ResourceRecords: []r53types.ResourceRecord{{Value: aws.String("192.0.2.1")}},
					},
				}},
			},
		})

		return err
	})

	sess.Op(svc, "ListRecords", func() error {
		out, err := client.ListResourceRecordSets(ctx, &awsr53.ListResourceRecordSetsInput{
			HostedZoneId: aws.String(zoneID),
		})
		if err != nil {
			return err
		}

		for _, rr := range out.ResourceRecordSets {
			if aws.ToString(rr.Name) == recName && rr.Type == r53types.RRTypeA {
				return nil
			}
		}

		return fmt.Errorf("record %q not found in ListResourceRecordSets", recName)
	})

	sess.Op(svc, "UpdateRecord", func() error {
		_, err := client.ChangeResourceRecordSets(ctx, &awsr53.ChangeResourceRecordSetsInput{
			HostedZoneId: aws.String(zoneID),
			ChangeBatch: &r53types.ChangeBatch{
				Changes: []r53types.Change{{
					Action: r53types.ChangeActionUpsert,
					ResourceRecordSet: &r53types.ResourceRecordSet{
						Name:            aws.String(recName),
						Type:            r53types.RRTypeA,
						TTL:             aws.Int64(600),
						ResourceRecords: []r53types.ResourceRecord{{Value: aws.String("192.0.2.2")}},
					},
				}},
			},
		})

		return err
	})

	sess.Op(svc, "DeleteRecord", func() error {
		_, err := client.ChangeResourceRecordSets(ctx, &awsr53.ChangeResourceRecordSetsInput{
			HostedZoneId: aws.String(zoneID),
			ChangeBatch: &r53types.ChangeBatch{
				Changes: []r53types.Change{{
					Action: r53types.ChangeActionDelete,
					ResourceRecordSet: &r53types.ResourceRecordSet{
						Name:            aws.String(recName),
						Type:            r53types.RRTypeA,
						TTL:             aws.Int64(600),
						ResourceRecords: []r53types.ResourceRecord{{Value: aws.String("192.0.2.2")}},
					},
				}},
			},
		})

		return err
	})

	var healthCheckID string

	sess.Op(svc, "CreateHealthCheck", func() error {
		out, err := client.CreateHealthCheck(ctx, &awsr53.CreateHealthCheckInput{
			CallerReference: aws.String("compat-hc-1"),
			HealthCheckConfig: &r53types.HealthCheckConfig{
				IPAddress:        aws.String("192.0.2.1"),
				Port:             aws.Int32(80),
				Type:             r53types.HealthCheckTypeHttp,
				ResourcePath:     aws.String("/health"),
				RequestInterval:  aws.Int32(30),
				FailureThreshold: aws.Int32(3),
			},
		})
		if err != nil {
			return err
		}

		if out.HealthCheck == nil || aws.ToString(out.HealthCheck.Id) == "" {
			return fmt.Errorf("CreateHealthCheck returned no id")
		}

		healthCheckID = aws.ToString(out.HealthCheck.Id)

		return nil
	})

	sess.Op(svc, "GetHealthCheck", func() error {
		_, err := client.GetHealthCheck(ctx, &awsr53.GetHealthCheckInput{HealthCheckId: aws.String(healthCheckID)})
		return err
	})

	sess.Op(svc, "ListHealthChecks", func() error {
		out, err := client.ListHealthChecks(ctx, &awsr53.ListHealthChecksInput{})
		if err != nil {
			return err
		}

		for i := range out.HealthChecks {
			if aws.ToString(out.HealthChecks[i].Id) == healthCheckID {
				return nil
			}
		}

		return fmt.Errorf("health check %q not found in ListHealthChecks", healthCheckID)
	})

	sess.Op(svc, "UpdateHealthCheck", func() error {
		_, err := client.UpdateHealthCheck(ctx, &awsr53.UpdateHealthCheckInput{
			HealthCheckId:    aws.String(healthCheckID),
			ResourcePath:     aws.String("/healthz"),
			FailureThreshold: aws.Int32(5),
		})

		return err
	})

	sess.Op(svc, "DeleteHealthCheck", func() error {
		_, err := client.DeleteHealthCheck(ctx, &awsr53.DeleteHealthCheckInput{HealthCheckId: aws.String(healthCheckID)})
		return err
	})

	sess.Op(svc, "DeleteZone", func() error {
		_, err := client.DeleteHostedZone(ctx, &awsr53.DeleteHostedZoneInput{Id: aws.String(zoneID)})
		return err
	})
}
