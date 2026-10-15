package aws

import (
	"context"
	"net/http"
	"strings"
	"testing"

	awsprovider "github.com/stackshy/cloudemu/v2/providers/aws"
	cfdriver "github.com/stackshy/cloudemu/v2/services/cloudfront/driver"
)

const (
	cfPath    = "/2020-05-31/distribution"
	cfARN     = "arn:aws:cloudfront::123456789012:distribution/"
	cfSigned  = "cloudfront"
	cfMinimal = `<DistributionConfig xmlns="http://cloudfront.amazonaws.com/doc/2020-05-31/">` +
		`<CallerReference>REF</CallerReference><Comment></Comment><Enabled>true</Enabled>` +
		`<Origins><Quantity>1</Quantity><Items><Origin><Id>o1</Id><DomainName>example.com</DomainName>` +
		`<CustomOriginConfig><HTTPPort>80</HTTPPort><HTTPSPort>443</HTTPSPort><OriginProtocolPolicy>https-only</OriginProtocolPolicy>` +
		`</CustomOriginConfig></Origin></Items></Origins><DefaultCacheBehavior><TargetOriginId>o1</TargetOriginId>` +
		`<ViewerProtocolPolicy>allow-all</ViewerProtocolPolicy></DefaultCacheBehavior></DistributionConfig>`
)

func newDistribution(t *testing.T, cloud *awsprovider.Provider, ref string, tags map[string]string) string {
	t.Helper()

	d, err := cloud.CloudFront.CreateDistribution(context.Background(), &cfdriver.CreateDistributionInput{
		CallerReference: ref, Enabled: true, Tags: tags,
	})
	if err != nil {
		t.Fatalf("CreateDistribution: %v", err)
	}

	return d.ID
}

func distributionCount(t *testing.T, cloud *awsprovider.Provider) int {
	t.Helper()

	ds, err := cloud.CloudFront.ListDistributions(context.Background())
	if err != nil {
		t.Fatalf("ListDistributions: %v", err)
	}

	return len(ds)
}

// TestAuthzMatrixCloudFront covers distribution ARNs, a single Deny, the
// aws:ResourceTag key, CreateDistributionWithTags needing TagResource, and a
// tagging ARN of another account.
func TestAuthzMatrixCloudFront(t *testing.T) {
	ts, cloud := matrixServer(t, nil)
	prod := newDistribution(t, cloud, "prod", map[string]string{"env": "prod"})
	dev := newDistribution(t, cloud, "dev", map[string]string{"env": "dev"})

	get := func(id string) sreq { return sreq{method: http.MethodGet, path: cfPath + "/" + id, service: cfSigned} }

	t.Run("one distribution in scope", func(t *testing.T) {
		u := userWithPolicy(t, cloud, "devreader", policyDoc(stmt("Allow", "cloudfront:GetDistribution", cfARN+dev)))

		status, body := doSigned(t, ts, u, get(dev))
		wantNotDenied(t, status, body)

		status, body = doSigned(t, ts, u, get(prod))
		wantDenied(t, status, body, "cloudfront:GetDistribution on resource: "+cfARN+prod)
	})

	t.Run("resource tag condition", func(t *testing.T) {
		u := userWithPolicy(t, cloud, "tagreader", policyDoc(
			stmtCond("Allow", "cloudfront:*", "*", `{"StringEquals":{"aws:ResourceTag/env":"dev"}}`)))

		status, body := doSigned(t, ts, u, sreq{method: http.MethodGet, path: cfPath + "/" + dev + "/invalidation", service: cfSigned})
		wantNotDenied(t, status, body)

		status, body = doSigned(t, ts, u, sreq{method: http.MethodGet, path: cfPath + "/" + prod + "/invalidation", service: cfSigned})
		wantDenied(t, status, body, xmlAccessDenied)
	})

	t.Run("deny one delete", func(t *testing.T) {
		u := userWithPolicy(t, cloud, "denydist", policyDoc(stmt("Allow", "*", "*"),
			stmt("Deny", "cloudfront:DeleteDistribution", cfARN+prod)))

		status, body := doSigned(t, ts, u, sreq{method: http.MethodDelete, path: cfPath + "/" + prod, service: cfSigned})
		wantDenied(t, status, body, "with an explicit deny")

		if _, err := cloud.CloudFront.GetDistribution(context.Background(), prod); err != nil {
			t.Fatal("a denied DeleteDistribution removed the distribution")
		}
	})

	t.Run("create with tags needs TagResource", func(t *testing.T) {
		withTags := sreq{path: cfPath + "?WithTags", ctype: "text/xml", service: cfSigned,
			body: `<DistributionConfigWithTags xmlns="http://cloudfront.amazonaws.com/doc/2020-05-31/">` +
				replaceRef(cfMinimal, "tagged") + `<Tags><Items><Tag><Key>team</Key><Value>a</Value></Tag></Items></Tags>` +
				`</DistributionConfigWithTags>`}

		before := distributionCount(t, cloud)

		creator := userWithPolicy(t, cloud, "creator", allow("cloudfront:CreateDistribution"))
		status, body := doSigned(t, ts, creator, withTags)
		wantDenied(t, status, body, "cloudfront:TagResource")

		if distributionCount(t, cloud) != before {
			t.Fatal("a denied CreateDistributionWithTags created a distribution")
		}

		plain := sreq{path: cfPath, ctype: "text/xml", service: cfSigned, body: replaceRef(cfMinimal, "plain")}
		if status, body = doSigned(t, ts, creator, plain); status != http.StatusCreated {
			t.Fatalf("CreateDistribution: %d %s", status, body)
		}

		tagger := userWithPolicy(t, cloud, "tagcreator", policyDoc(
			stmt("Allow", "cloudfront:CreateDistribution", "*"),
			stmtCond("Allow", "cloudfront:TagResource", cfARN+"*", `{"StringEquals":{"aws:RequestTag/team":"a"}}`)))
		if status, body = doSigned(t, ts, tagger, withTags); status != http.StatusCreated {
			t.Fatalf("CreateDistributionWithTags: %d %s", status, body)
		}

		if got := distributionCount(t, cloud); got != before+2 {
			t.Fatalf("distributions = %d, want %d", got, before+2)
		}
	})

	t.Run("tagging ARN of another account", func(t *testing.T) {
		u := userWithPolicy(t, cloud, "cftagger", allow("cloudfront:ListTagsForResource"))

		status, body := doSigned(t, ts, u, sreq{method: http.MethodGet, service: cfSigned,
			path: "/2020-05-31/tagging?Resource=arn:aws:cloudfront::999999999999:distribution/" + dev})
		wantDenied(t, status, body, xmlAccessDenied)

		status, body = doSigned(t, ts, u, sreq{method: http.MethodGet, service: cfSigned,
			path: "/2020-05-31/tagging?Resource=" + cfARN + dev})
		wantNotDenied(t, status, body)
	})
}

func replaceRef(cfg, ref string) string {
	return strings.Replace(cfg, "<CallerReference>REF</CallerReference>", "<CallerReference>"+ref+"</CallerReference>", 1)
}
