package configservice

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/configservice/driver"
)

// TestSnapshotRoundTripConfigService proves a snapshot/restore round-trip
// preserves the configuration recorder, a config rule (a promoted store whose
// value carries unexported evaluations/result-token state), and a conformance
// pack under their original identities.
func TestSnapshotRoundTripConfigService(t *testing.T) {
	ctx := context.Background()
	src := newMock(t)

	putRecorder(t, src, "default")

	if err := src.PutConfigRule(ctx, driver.ConfigRule{
		ConfigRuleName: "rule1",
		Source:         &driver.RuleSource{Owner: "AWS", SourceIdentifier: "S3_BUCKET_PUBLIC_READ_PROHIBITED"},
	}); err != nil {
		t.Fatalf("PutConfigRule: %v", err)
	}

	if _, err := src.PutConformancePack(ctx, driver.ConformancePack{
		ConformancePackName: "pack1", DeliveryS3Bucket: "bkt", TemplateBody: "Resources: {}",
	}); err != nil {
		t.Fatalf("PutConformancePack: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newMock(t)
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	recs, err := dst.DescribeConfigurationRecorders(ctx, nil)
	if err != nil || len(recs) != 1 || recs[0].Name != "default" {
		t.Fatalf("restored recorders = %+v, err %v", recs, err)
	}

	rules, _, err := dst.DescribeConfigRules(ctx, nil, driver.Page{})
	if err != nil || len(rules) != 1 || rules[0].ConfigRuleName != "rule1" {
		t.Fatalf("restored rules = %+v, err %v", rules, err)
	}

	if rules[0].Source == nil || rules[0].Source.SourceIdentifier != "S3_BUCKET_PUBLIC_READ_PROHIBITED" {
		t.Fatalf("restored rule source = %+v", rules[0].Source)
	}

	packs, _, err := dst.DescribeConformancePacks(ctx, nil, driver.Page{})
	if err != nil || len(packs) != 1 || packs[0].ConformancePackName != "pack1" {
		t.Fatalf("restored packs = %+v, err %v", packs, err)
	}
}
