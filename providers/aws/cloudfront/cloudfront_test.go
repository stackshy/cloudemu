package cloudfront_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudfront"
	"github.com/stackshy/cloudemu/v2/services/cloudfront/driver"
)

func newMock() *cloudfront.Mock {
	return cloudfront.New(config.NewOptions(config.WithAccountID("123456789012")))
}

const sampleConfig = `<CallerReference>ref-1</CallerReference>` +
	`<Comment>hello</Comment>` +
	`<Enabled>true</Enabled>` +
	`<Origins><Quantity>1</Quantity><Items><Origin><Id>o1</Id>` +
	`<DomainName>b.s3.amazonaws.com</DomainName><S3OriginConfig>` +
	`<OriginAccessIdentity></OriginAccessIdentity></S3OriginConfig></Origin></Items></Origins>` +
	`<PriceClass>PriceClass_All</PriceClass>`

func createSample(t *testing.T, m *cloudfront.Mock, ref string, enabled bool) *driver.Distribution {
	t.Helper()

	cfg := strings.Replace(sampleConfig, "ref-1", ref, 1)
	if !enabled {
		cfg = strings.Replace(cfg, "<Enabled>true</Enabled>", "<Enabled>false</Enabled>", 1)
	}

	dist, err := m.CreateDistribution(context.Background(), &driver.CreateDistributionInput{
		CallerReference: ref,
		Enabled:         enabled,
		Comment:         "hello",
		ConfigXML:       []byte(cfg),
	})
	if err != nil {
		t.Fatalf("CreateDistribution: %v", err)
	}

	return dist
}

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateDistributionAssignsIdentity(t *testing.T) {
	m := newMock()
	dist := createSample(t, m, "ref-1", true)

	if len(dist.ID) != 14 || dist.ID[0] != 'E' {
		t.Errorf("id = %q, want 14-char E-prefixed", dist.ID)
	}

	if dist.Status != driver.StatusDeployed {
		t.Errorf("status = %q, want Deployed", dist.Status)
	}

	if !strings.HasSuffix(dist.DomainName, ".cloudfront.net") {
		t.Errorf("domain = %q, want .cloudfront.net", dist.DomainName)
	}

	wantARN := "arn:aws:cloudfront::123456789012:distribution/" + dist.ID
	if dist.ARN != wantARN {
		t.Errorf("arn = %q, want %q", dist.ARN, wantARN)
	}

	if dist.ETag == "" {
		t.Error("etag is empty")
	}
}

func TestCreateDistributionDuplicateCallerReference(t *testing.T) {
	m := newMock()
	createSample(t, m, "ref-1", true)

	_, err := m.CreateDistribution(context.Background(), &driver.CreateDistributionInput{
		CallerReference: "ref-1",
		ConfigXML:       []byte(sampleConfig),
	})
	if !errors.Is(err, driver.ErrDistributionAlreadyExists) {
		t.Fatalf("err = %v, want ErrDistributionAlreadyExists", err)
	}
}

func TestGetDistributionRoundTripsConfig(t *testing.T) {
	m := newMock()
	created := createSample(t, m, "ref-1", true)

	got, err := m.GetDistribution(context.Background(), created.ID)
	requireNoError(t, err)

	if string(got.ConfigXML) != strings.Replace(sampleConfig, "ref-1", "ref-1", 1) {
		t.Errorf("config not round-tripped:\n got %s", got.ConfigXML)
	}

	if _, err := m.GetDistribution(context.Background(), "EDOESNOTEXIST"); !errors.Is(err, driver.ErrNoSuchDistribution) {
		t.Fatalf("err = %v, want ErrNoSuchDistribution", err)
	}
}

func TestUpdateDistributionETagConcurrency(t *testing.T) {
	m := newMock()
	dist := createSample(t, m, "ref-1", true)

	// Missing If-Match.
	_, err := m.UpdateDistribution(context.Background(), &driver.UpdateDistributionInput{
		ID: dist.ID, IfMatch: "", CallerReference: "ref-1", ConfigXML: dist.ConfigXML,
	})
	if !errors.Is(err, driver.ErrInvalidIfMatchVersion) {
		t.Fatalf("missing if-match err = %v, want ErrInvalidIfMatchVersion", err)
	}

	// Wrong If-Match.
	_, err = m.UpdateDistribution(context.Background(), &driver.UpdateDistributionInput{
		ID: dist.ID, IfMatch: "WRONGETAG", CallerReference: "ref-1", ConfigXML: dist.ConfigXML,
	})
	if !errors.Is(err, driver.ErrPreconditionFailed) {
		t.Fatalf("wrong if-match err = %v, want ErrPreconditionFailed", err)
	}

	// Changing CallerReference is rejected.
	_, err = m.UpdateDistribution(context.Background(), &driver.UpdateDistributionInput{
		ID: dist.ID, IfMatch: dist.ETag, CallerReference: "ref-2", ConfigXML: dist.ConfigXML,
	})
	if !errors.Is(err, driver.ErrCallerReferenceImmutable) {
		t.Fatalf("caller-ref-change err = %v, want ErrCallerReferenceImmutable", err)
	}

	// Correct update disables and rotates the ETag.
	newCfg := strings.Replace(string(dist.ConfigXML), "<Enabled>true</Enabled>", "<Enabled>false</Enabled>", 1)
	updated, err := m.UpdateDistribution(context.Background(), &driver.UpdateDistributionInput{
		ID: dist.ID, IfMatch: dist.ETag, CallerReference: "ref-1", Enabled: false, ConfigXML: []byte(newCfg),
	})
	requireNoError(t, err)

	if updated.ETag == dist.ETag {
		t.Error("etag did not rotate on update")
	}

	if updated.Enabled {
		t.Error("distribution should be disabled after update")
	}
}

func TestDeleteDistributionGuards(t *testing.T) {
	m := newMock()
	dist := createSample(t, m, "ref-1", true)

	// Missing If-Match.
	if err := m.DeleteDistribution(context.Background(), dist.ID, ""); !errors.Is(err, driver.ErrInvalidIfMatchVersion) {
		t.Fatalf("missing if-match err = %v, want ErrInvalidIfMatchVersion", err)
	}

	// Wrong If-Match.
	if err := m.DeleteDistribution(context.Background(), dist.ID, "WRONG"); !errors.Is(err, driver.ErrPreconditionFailed) {
		t.Fatalf("wrong if-match err = %v, want ErrPreconditionFailed", err)
	}

	// Still enabled.
	if err := m.DeleteDistribution(context.Background(), dist.ID, dist.ETag); !errors.Is(err, driver.ErrDistributionNotDisabled) {
		t.Fatalf("enabled delete err = %v, want ErrDistributionNotDisabled", err)
	}

	// Disable, then delete.
	updated, err := m.UpdateDistribution(context.Background(), &driver.UpdateDistributionInput{
		ID: dist.ID, IfMatch: dist.ETag, CallerReference: "ref-1", Enabled: false, ConfigXML: dist.ConfigXML,
	})
	requireNoError(t, err)
	requireNoError(t, m.DeleteDistribution(context.Background(), dist.ID, updated.ETag))

	if _, err := m.GetDistribution(context.Background(), dist.ID); !errors.Is(err, driver.ErrNoSuchDistribution) {
		t.Fatalf("get after delete err = %v, want ErrNoSuchDistribution", err)
	}
}

func TestListDistributionsDeterministicOrder(t *testing.T) {
	m := newMock()
	a := createSample(t, m, "ref-a", true)
	b := createSample(t, m, "ref-b", true)
	c := createSample(t, m, "ref-c", true)

	list, err := m.ListDistributions(context.Background())
	requireNoError(t, err)

	if len(list) != 3 {
		t.Fatalf("len = %d, want 3", len(list))
	}

	if list[0].ID != a.ID || list[1].ID != b.ID || list[2].ID != c.ID {
		t.Errorf("order = %s,%s,%s; want creation order %s,%s,%s",
			list[0].ID, list[1].ID, list[2].ID, a.ID, b.ID, c.ID)
	}
}

func TestInvalidationSynchronous(t *testing.T) {
	m := newMock()
	dist := createSample(t, m, "ref-1", true)

	inv, err := m.CreateInvalidation(context.Background(), dist.ID, &driver.CreateInvalidationInput{
		CallerReference: "inv-1",
		Paths:           []string{"/*", "/index.html"},
	})
	requireNoError(t, err)

	if inv.Status != driver.InvalidationCompleted {
		t.Errorf("status = %q, want Completed", inv.Status)
	}

	got, err := m.GetInvalidation(context.Background(), dist.ID, inv.ID)
	requireNoError(t, err)

	if len(got.Paths) != 2 {
		t.Errorf("paths = %v, want 2", got.Paths)
	}

	list, err := m.ListInvalidations(context.Background(), dist.ID)
	requireNoError(t, err)

	if len(list) != 1 {
		t.Errorf("list len = %d, want 1", len(list))
	}

	if _, err := m.CreateInvalidation(context.Background(), "EMISSING", &driver.CreateInvalidationInput{}); !errors.Is(err, driver.ErrNoSuchDistribution) {
		t.Fatalf("missing dist err = %v, want ErrNoSuchDistribution", err)
	}
}

func TestTags(t *testing.T) {
	m := newMock()
	dist := createSample(t, m, "ref-1", true)

	requireNoError(t, m.TagResource(context.Background(), dist.ARN, map[string]string{"env": "prod", "team": "web"}))

	tags, err := m.ListTagsForResource(context.Background(), dist.ARN)
	requireNoError(t, err)

	if tags["env"] != "prod" || tags["team"] != "web" {
		t.Errorf("tags = %v", tags)
	}

	requireNoError(t, m.UntagResource(context.Background(), dist.ARN, []string{"team"}))

	tags, err = m.ListTagsForResource(context.Background(), dist.ARN)
	requireNoError(t, err)

	if _, ok := tags["team"]; ok {
		t.Errorf("team tag not removed: %v", tags)
	}

	if err := m.TagResource(context.Background(), "arn:aws:cloudfront::123456789012:distribution/EMISSING", nil); !errors.Is(err, driver.ErrNoSuchDistribution) {
		t.Fatalf("missing dist err = %v, want ErrNoSuchDistribution", err)
	}
}

func TestCreateDistributionWithTags(t *testing.T) {
	m := newMock()

	dist, err := m.CreateDistribution(context.Background(), &driver.CreateDistributionInput{
		CallerReference: "ref-1",
		Enabled:         true,
		ConfigXML:       []byte(sampleConfig),
		Tags:            map[string]string{"env": "prod"},
	})
	requireNoError(t, err)

	tags, err := m.ListTagsForResource(context.Background(), dist.ARN)
	requireNoError(t, err)

	if tags["env"] != "prod" {
		t.Errorf("tags = %v, want env=prod", tags)
	}
}
