package aws

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/stackshy/cloudemu/v2/providers/aws/ecs"
	"github.com/stackshy/cloudemu/v2/providers/aws/ssm"
	bedrockdriver "github.com/stackshy/cloudemu/v2/services/bedrock/driver"
	ecsdriver "github.com/stackshy/cloudemu/v2/services/ecs/driver"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
	r53rdriver "github.com/stackshy/cloudemu/v2/services/route53resolver/driver"
	sagemakerdriver "github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

// awsTaggers builds the Resource Groups Tagging API generic-tagger registry: an
// ARN-service-token → adapter map that lets TagResources/UntagResources reach
// every AWS mock's own tag store, beyond the dozen services the discovery
// drivers already cover. Each adapter bridges the RGT ARN to whatever key the
// service's tag store uses (full ARN, bare id/name) and calls the service's
// real tag method, so a tag applied via RGT is visible through that service's
// own ListTagsForResource/Describe surface (and, for the discoverable services,
// through GetResources). Split into grouped builders to stay within the
// per-function length budget.
func awsTaggers(p *Provider) map[string]resourcediscovery.ARNTagger {
	const taggerCount = 28

	m := make(map[string]resourcediscovery.ARNTagger, taggerCount)
	addKeyedTaggers(m, p)
	addNamedMapTaggers(m, p)
	addTypedTagTaggers(m, p)
	addSpecialTaggers(m, p)

	return m
}

// mapTagStore is the shape shared by most AWS mock tag stores: a set/remove pair
// addressed by a single string key with map-valued tags.
type mapTagStore interface {
	TagResource(ctx context.Context, key string, tags map[string]string) error
	UntagResource(ctx context.Context, key string, keys []string) error
}

// keyedTagger adapts a mapTagStore to resourcediscovery.ARNTagger, deriving the
// store key from the RGT ARN via key (use fullARN for stores keyed by the ARN).
type keyedTagger struct {
	store mapTagStore
	key   func(arn string) string
}

func (k keyedTagger) TagByARN(ctx context.Context, arn string, tags map[string]string) error {
	return k.store.TagResource(ctx, k.key(arn), tags)
}

func (k keyedTagger) UntagByARN(ctx context.Context, arn string, keys []string) error {
	return k.store.UntagResource(ctx, k.key(arn), keys)
}

// funcTagger adapts arbitrary tag/untag closures to resourcediscovery.ARNTagger,
// for services whose tag methods have irregular names, tag types, or returns.
type funcTagger struct {
	tag   func(ctx context.Context, arn string, tags map[string]string) error
	untag func(ctx context.Context, arn string, keys []string) error
}

func (f funcTagger) TagByARN(ctx context.Context, arn string, tags map[string]string) error {
	return f.tag(ctx, arn, tags)
}

func (f funcTagger) UntagByARN(ctx context.Context, arn string, keys []string) error {
	return f.untag(ctx, arn, keys)
}

// addKeyedTaggers registers services whose mock exposes the canonical
// TagResource/UntagResource(key, …) pair. Most key by the full ARN; EFS keys by
// the file-system/access-point id and KMS by the key id, so those two supply a
// key extractor.
func addKeyedTaggers(m map[string]resourcediscovery.ARNTagger, p *Provider) {
	m["config"] = keyedTagger{p.Config, fullARN}
	m["ecs"] = keyedTagger{ecsMapStore{p.ECS}, fullARN}
	m["eks"] = keyedTagger{p.EKS, fullARN}
	m["events"] = keyedTagger{p.EventBridge, fullARN}
	m["glue"] = keyedTagger{p.Glue, fullARN}
	m["kafka"] = keyedTagger{p.Kafka, fullARN}
	m["cassandra"] = keyedTagger{p.Keyspaces, fullARN}
	m["kinesis"] = keyedTagger{p.Kinesis, fullARN}
	m["network-firewall"] = keyedTagger{p.NetworkFirewall, fullARN}
	m["ses"] = keyedTagger{p.SESV2, fullARN}
	m["states"] = keyedTagger{p.SFN, fullARN}
	m["vpc-lattice"] = keyedTagger{p.VPCLattice, fullARN}
	m["wafv2"] = keyedTagger{p.WAFv2, fullARN}
	m["elasticfilesystem"] = keyedTagger{p.EFS, lastPathSegment}
	m["kms"] = keyedTagger{p.KMS, kmsKeyID}
	m["ssm"] = keyedTagger{ssmParamStore{p.SSM}, ssmParamName}
}

// addNamedMapTaggers registers services with map-valued tags whose mock uses a
// non-canonical method name (or, for CloudWatch, keys by the alarm name derived
// from the ARN).
func addNamedMapTaggers(m map[string]resourcediscovery.ARNTagger, p *Provider) {
	m["acm"] = funcTagger{
		tag: func(ctx context.Context, arn string, tags map[string]string) error {
			return p.ACM.AddTagsToCertificate(ctx, arn, tags)
		},
		untag: func(ctx context.Context, arn string, keys []string) error {
			return p.ACM.RemoveTagsFromCertificate(ctx, arn, keys)
		},
	}
	m["cloudtrail"] = funcTagger{
		tag: func(ctx context.Context, arn string, tags map[string]string) error {
			return p.CloudTrail.AddTags(ctx, arn, tags)
		},
		untag: func(ctx context.Context, arn string, keys []string) error {
			return p.CloudTrail.RemoveTags(ctx, arn, keys)
		},
	}
	m["cloudwatch"] = funcTagger{
		tag: func(ctx context.Context, arn string, tags map[string]string) error {
			return p.CloudWatch.AddAlarmTags(ctx, cwAlarmName(arn), tags)
		},
		untag: func(ctx context.Context, arn string, keys []string) error {
			return p.CloudWatch.RemoveAlarmTags(ctx, cwAlarmName(arn), keys)
		},
	}
	m["es"] = funcTagger{
		tag: func(ctx context.Context, arn string, tags map[string]string) error {
			return p.OpenSearch.AddTags(ctx, arn, tags)
		},
		untag: func(ctx context.Context, arn string, keys []string) error {
			return p.OpenSearch.RemoveTags(ctx, arn, keys)
		},
	}
	m["redshift"] = funcTagger{
		tag: func(ctx context.Context, arn string, tags map[string]string) error {
			return p.Redshift.CreateTags(ctx, arn, tags)
		},
		untag: func(ctx context.Context, arn string, keys []string) error {
			return p.Redshift.DeleteTags(ctx, arn, keys)
		},
	}
	m["elasticloadbalancing"] = funcTagger{
		tag: func(ctx context.Context, arn string, tags map[string]string) error {
			return p.ELB.AddResourceTags(ctx, arn, tags)
		},
		untag: func(ctx context.Context, arn string, keys []string) error {
			return p.ELB.RemoveResourceTags(ctx, arn, keys)
		},
	}
}

// addTypedTagTaggers registers services whose mock takes a []driver.Tag slice
// instead of a map; the closures convert the RGT map into the service's own Tag
// type.
func addTypedTagTaggers(m map[string]resourcediscovery.ARNTagger, p *Provider) {
	m["bedrock"] = funcTagger{
		tag: func(ctx context.Context, arn string, tags map[string]string) error {
			return p.Bedrock.TagResource(ctx, arn, bedrockTags(tags))
		},
		untag: func(ctx context.Context, arn string, keys []string) error {
			return p.Bedrock.UntagResource(ctx, arn, keys)
		},
	}
	m["route53resolver"] = funcTagger{
		tag: func(ctx context.Context, arn string, tags map[string]string) error {
			return p.Route53Resolver.TagResource(ctx, arn, route53ResolverTags(tags))
		},
		untag: func(ctx context.Context, arn string, keys []string) error {
			return p.Route53Resolver.UntagResource(ctx, arn, keys)
		},
	}
	m["sagemaker"] = funcTagger{
		tag: func(ctx context.Context, arn string, tags map[string]string) error {
			_, err := p.SageMaker.AddTags(ctx, arn, sagemakerTags(tags))

			return err
		},
		untag: func(ctx context.Context, arn string, keys []string) error {
			return p.SageMaker.DeleteTags(ctx, arn, keys)
		},
	}
}

// addSpecialTaggers registers services whose tag surface does not fit the other
// shapes: GuardDuty (JSON-body tags + JSON returns), MemoryDB (returns the
// resulting tag list), and Route 53 (a single combined add/remove call keyed by
// the bare hosted-zone id).
func addSpecialTaggers(m map[string]resourcediscovery.ARNTagger, p *Provider) {
	m["guardduty"] = funcTagger{
		tag: func(ctx context.Context, arn string, tags map[string]string) error {
			body, err := json.Marshal(map[string]map[string]string{"tags": tags})
			if err != nil {
				return err
			}

			_, err = p.GuardDuty.TagResource(ctx, arn, body)

			return err
		},
		untag: func(ctx context.Context, arn string, keys []string) error {
			_, err := p.GuardDuty.UntagResource(ctx, arn, keys)

			return err
		},
	}
	m["memorydb"] = funcTagger{
		tag: func(ctx context.Context, arn string, tags map[string]string) error {
			_, err := p.MemoryDB.TagResource(ctx, arn, tags)

			return err
		},
		untag: func(ctx context.Context, arn string, keys []string) error {
			_, err := p.MemoryDB.UntagResource(ctx, arn, keys)

			return err
		},
	}
	m["route53"] = funcTagger{
		tag: func(ctx context.Context, arn string, tags map[string]string) error {
			return p.Route53.ChangeResourceTags(ctx, hostedZoneID(arn), tags, nil)
		},
		untag: func(ctx context.Context, arn string, keys []string) error {
			return p.Route53.ChangeResourceTags(ctx, hostedZoneID(arn), nil, keys)
		},
	}
}

// ecsMapStore adapts the ECS mock ([]driver.Tag) to the map-keyed mapTagStore.
type ecsMapStore struct{ m *ecs.Mock }

func (e ecsMapStore) TagResource(ctx context.Context, arn string, tags map[string]string) error {
	return e.m.TagResource(ctx, arn, ecsTags(tags))
}

func (e ecsMapStore) UntagResource(ctx context.Context, arn string, keys []string) error {
	return e.m.UntagResource(ctx, arn, keys)
}

// ssmParamStore adapts the SSM parameter tag methods (TagParameter/
// UntagParameter) to mapTagStore.
type ssmParamStore struct{ m *ssm.Mock }

func (s ssmParamStore) TagResource(ctx context.Context, name string, tags map[string]string) error {
	return s.m.TagParameter(ctx, name, tags)
}

func (s ssmParamStore) UntagResource(ctx context.Context, name string, keys []string) error {
	return s.m.UntagParameter(ctx, name, keys)
}

// fullARN is the identity key extractor for stores addressed by the full ARN.
func fullARN(arn string) string { return arn }

// lastPathSegment returns the segment after the final '/', the id EFS keys its
// tag store by (fs-… / fsap-…) within a file-system/access-point ARN.
func lastPathSegment(arn string) string {
	if i := strings.LastIndexByte(arn, '/'); i >= 0 {
		return arn[i+1:]
	}

	return arn
}

// kmsKeyID returns the key id KMS tags by — the segment after ":key/" in a KMS
// ARN (arn:aws:kms:…:key/<id>).
func kmsKeyID(arn string) string {
	const marker = ":key/"
	if i := strings.Index(arn, marker); i >= 0 {
		return arn[i+len(marker):]
	}

	return lastPathSegment(arn)
}

// ssmParamName returns the parameter name SSM keys its tag store by — the
// segment after ":parameter/" in an SSM parameter ARN.
func ssmParamName(arn string) string {
	const marker = ":parameter/"
	if i := strings.Index(arn, marker); i >= 0 {
		return arn[i+len(marker):]
	}

	return arn
}

// cwAlarmName returns the alarm name CloudWatch keys its tag store by — the
// segment after ":alarm:" in a CloudWatch alarm ARN (arn:aws:cloudwatch:…:alarm:<name>).
func cwAlarmName(arn string) string {
	const marker = ":alarm:"
	if i := strings.Index(arn, marker); i >= 0 {
		return arn[i+len(marker):]
	}

	return arn
}

// hostedZoneID returns the Route 53 hosted-zone id the tag store keys by. RGT
// may present either the bare id (as the discovery walk surfaces it) or a real
// arn:aws:route53:::hostedzone/<id> ARN.
func hostedZoneID(arn string) string {
	const marker = "hostedzone/"
	if i := strings.Index(arn, marker); i >= 0 {
		return arn[i+len(marker):]
	}

	return arn
}

func bedrockTags(tags map[string]string) []bedrockdriver.Tag {
	out := make([]bedrockdriver.Tag, 0, len(tags))
	for k, v := range tags {
		out = append(out, bedrockdriver.Tag{Key: k, Value: v})
	}

	return out
}

func ecsTags(tags map[string]string) []ecsdriver.Tag {
	out := make([]ecsdriver.Tag, 0, len(tags))
	for k, v := range tags {
		out = append(out, ecsdriver.Tag{Key: k, Value: v})
	}

	return out
}

func route53ResolverTags(tags map[string]string) []r53rdriver.Tag {
	out := make([]r53rdriver.Tag, 0, len(tags))
	for k, v := range tags {
		out = append(out, r53rdriver.Tag{Key: k, Value: v})
	}

	return out
}

func sagemakerTags(tags map[string]string) []sagemakerdriver.Tag {
	out := make([]sagemakerdriver.Tag, 0, len(tags))
	for k, v := range tags {
		out = append(out, sagemakerdriver.Tag{Key: k, Value: v})
	}

	return out
}
