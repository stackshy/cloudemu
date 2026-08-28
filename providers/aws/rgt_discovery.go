package aws

import (
	"context"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	acmdriver "github.com/stackshy/cloudemu/v2/services/acm/driver"
	cloudtraildriver "github.com/stackshy/cloudemu/v2/services/cloudtrail/driver"
	configservicedriver "github.com/stackshy/cloudemu/v2/services/configservice/driver"
	ecsdriver "github.com/stackshy/cloudemu/v2/services/ecs/driver"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	gluedriver "github.com/stackshy/cloudemu/v2/services/glue/driver"
	guarddutydriver "github.com/stackshy/cloudemu/v2/services/guardduty/driver"
	kafkadriver "github.com/stackshy/cloudemu/v2/services/kafka/driver"
	ksdriver "github.com/stackshy/cloudemu/v2/services/keyspaces/driver"
	kmsdriver "github.com/stackshy/cloudemu/v2/services/kms/driver"
	mdbdriver "github.com/stackshy/cloudemu/v2/services/memorydb/driver"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
	ssmdriver "github.com/stackshy/cloudemu/v2/services/parameterstore/driver"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
	r53rdriver "github.com/stackshy/cloudemu/v2/services/route53resolver/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
	sesv2driver "github.com/stackshy/cloudemu/v2/services/sesv2/driver"
	sfndriver "github.com/stackshy/cloudemu/v2/services/sfn/driver"
	vpclatticedriver "github.com/stackshy/cloudemu/v2/services/vpclattice/driver"
	wafv2driver "github.com/stackshy/cloudemu/v2/services/wafv2/driver"
)

// taggedDiscovery projects the provider-native services that carry their own
// tag store — but are not reached by the portable-driver discovery walkers —
// into the cross-service inventory, so their resources surface in Resource
// Groups Tagging API GetResources with the ARN, service token, type, and live
// tags a tagger writes. This closes the discover → tag → filter → untag loop
// for these services. Each emitted ARN matches the shape awsTaggers routes on,
// and each Tags value is read from the same store the tagger mutates, so a tag
// applied via RGT reappears on the next GetResources.
type taggedDiscovery struct{ p *Provider }

// pageLimit is a large upper bound passed to paginated list methods so a single
// page returns every resource; the loops still follow NextToken defensively.
const pageLimit = 1000

func (d taggedDiscovery) collectors() []func(context.Context) ([]resourcediscovery.DiscoveredResource, error) {
	return []func(context.Context) ([]resourcediscovery.DiscoveredResource, error){
		d.discoverECS, d.discoverGlue, d.discoverKafka, d.discoverOpenSearch,
		d.discoverSFN, d.discoverSSM, d.discoverKMS, d.discoverKinesis,
		d.discoverGuardDuty, d.discoverCloudTrail, d.discoverConfig, d.discoverWAFv2,
		d.discoverNetworkFirewall, d.discoverSESv2, d.discoverVPCLattice, d.discoverMemoryDB,
		d.discoverKeyspaces, d.discoverResolver, d.discoverEventBridge, d.discoverACM,
	}
}

func (d taggedDiscovery) DiscoverResources(ctx context.Context) ([]resourcediscovery.DiscoveredResource, error) {
	var out []resourcediscovery.DiscoveredResource

	for _, collect := range d.collectors() {
		rows, err := collect(ctx)
		if err != nil {
			return nil, err
		}

		out = append(out, rows...)
	}

	return out, nil
}

func (d taggedDiscovery) discoverECS(ctx context.Context) ([]resourcediscovery.DiscoveredResource, error) {
	clusters, err := d.p.ECS.ListClusters(ctx)
	if err != nil {
		return nil, err
	}

	return project(ctx, clusters, func(ctx context.Context, c ecsdriver.Cluster) (resourcediscovery.DiscoveredResource, error) {
		tags, err := d.p.ECS.ListTagsForResource(ctx, c.ARN)

		return row("ecs", "cluster", c.Name, c.ARN, tagsToMap(tags, ecsKV)), err
	})
}

func (d taggedDiscovery) discoverOpenSearch(ctx context.Context) ([]resourcediscovery.DiscoveredResource, error) {
	domains, err := d.p.OpenSearch.ListDomainNames(ctx, "")
	if err != nil {
		return nil, err
	}

	out := make([]resourcediscovery.DiscoveredResource, 0, len(domains))

	for i := range domains {
		name := domains[i].DomainName
		arn := idgen.AWSARN("es", d.p.Region, d.p.AccountID, "domain/"+name)

		tags, err := d.p.OpenSearch.ListTags(ctx, arn)
		if err != nil {
			return nil, err
		}

		out = append(out, row("es", "domain", name, arn, tags))
	}

	return out, nil
}

func (d taggedDiscovery) discoverSFN(ctx context.Context) ([]resourcediscovery.DiscoveredResource, error) {
	machines, err := d.p.SFN.ListStateMachines(ctx)
	if err != nil {
		return nil, err
	}

	return project(ctx, machines, func(ctx context.Context, s sfndriver.StateMachine) (resourcediscovery.DiscoveredResource, error) {
		tags, err := d.p.SFN.ListTagsForResource(ctx, s.ARN)

		return row("states", "stateMachine", s.Name, s.ARN, tags), err
	})
}

func (d taggedDiscovery) discoverSSM(ctx context.Context) ([]resourcediscovery.DiscoveredResource, error) {
	params, err := d.p.SSM.DescribeParameters(ctx)
	if err != nil {
		return nil, err
	}

	return project(ctx, params, func(ctx context.Context, p ssmdriver.ParameterMetadata) (resourcediscovery.DiscoveredResource, error) {
		tags, err := d.p.SSM.ListParameterTags(ctx, p.Name)

		return row("ssm", "parameter", p.Name, p.ARN, tags), err
	})
}

func (d taggedDiscovery) discoverKMS(ctx context.Context) ([]resourcediscovery.DiscoveredResource, error) {
	keys, err := d.p.KMS.ListKeys(ctx)
	if err != nil {
		return nil, err
	}

	return project(ctx, keys, func(ctx context.Context, k kmsdriver.KeyMetadata) (resourcediscovery.DiscoveredResource, error) {
		tags, err := d.p.KMS.ListResourceTags(ctx, k.KeyID)

		return row("kms", "key", k.KeyID, k.ARN, tags), err
	})
}

func (d taggedDiscovery) discoverNetworkFirewall(ctx context.Context) ([]resourcediscovery.DiscoveredResource, error) {
	firewalls, err := d.p.NetworkFirewall.ListFirewalls(ctx)
	if err != nil {
		return nil, err
	}

	return project(ctx, firewalls, func(_ context.Context, f nfdriver.Firewall) (resourcediscovery.DiscoveredResource, error) {
		return row("network-firewall", "firewall", f.Name, f.ARN, f.Tags), nil
	})
}

func (d taggedDiscovery) discoverSESv2(ctx context.Context) ([]resourcediscovery.DiscoveredResource, error) {
	identities, err := d.p.SESV2.ListEmailIdentities(ctx)
	if err != nil {
		return nil, err
	}

	return project(ctx, identities, func(_ context.Context, id sesv2driver.Identity) (resourcediscovery.DiscoveredResource, error) {
		arn := idgen.AWSARN("ses", d.p.Region, d.p.AccountID, "identity/"+id.Name)

		return row("ses", "identity", id.Name, arn, id.Tags), nil
	})
}

func (d taggedDiscovery) discoverVPCLattice(ctx context.Context) ([]resourcediscovery.DiscoveredResource, error) {
	services, err := d.p.VPCLattice.ListServices(ctx)
	if err != nil {
		return nil, err
	}

	return project(ctx, services, func(ctx context.Context, s vpclatticedriver.Service) (resourcediscovery.DiscoveredResource, error) {
		tags, err := d.p.VPCLattice.ListTagsForResource(ctx, s.ARN)

		return row("vpc-lattice", "service", s.ID, s.ARN, tags), err
	})
}

func (d taggedDiscovery) discoverMemoryDB(ctx context.Context) ([]resourcediscovery.DiscoveredResource, error) {
	clusters, err := d.p.MemoryDB.DescribeClusters(ctx, nil)
	if err != nil {
		return nil, err
	}

	return project(ctx, clusters, func(ctx context.Context, c mdbdriver.Cluster) (resourcediscovery.DiscoveredResource, error) {
		tags, err := d.p.MemoryDB.ListTags(ctx, c.ARN)

		return row("memorydb", "cluster", c.Name, c.ARN, tagsToMap(tags, mdbKV)), err
	})
}

func (d taggedDiscovery) discoverKeyspaces(ctx context.Context) ([]resourcediscovery.DiscoveredResource, error) {
	keyspaces, err := d.p.Keyspaces.ListKeyspaces(ctx)
	if err != nil {
		return nil, err
	}

	return project(ctx, keyspaces, func(ctx context.Context, k ksdriver.Keyspace) (resourcediscovery.DiscoveredResource, error) {
		tags, err := d.p.Keyspaces.ListTagsForResource(ctx, k.ARN)

		return row("cassandra", "keyspace", k.Name, k.ARN, tagsToMap(tags, ksKV)), err
	})
}

func (d taggedDiscovery) discoverResolver(ctx context.Context) ([]resourcediscovery.DiscoveredResource, error) {
	endpoints, err := d.p.Route53Resolver.ListResolverEndpoints(ctx)
	if err != nil {
		return nil, err
	}

	return project(ctx, endpoints, func(ctx context.Context, e r53rdriver.ResolverEndpoint) (resourcediscovery.DiscoveredResource, error) {
		tags, err := d.p.Route53Resolver.ListTagsForResource(ctx, e.ARN)

		return row("route53resolver", "resolver-endpoint", e.ID, e.ARN, tagsToMap(tags, r53rKV)), err
	})
}

func (d taggedDiscovery) discoverEventBridge(ctx context.Context) ([]resourcediscovery.DiscoveredResource, error) {
	buses, err := d.p.EventBridge.ListEventBuses(ctx, scope.Scope{})
	if err != nil {
		return nil, err
	}

	return project(ctx, buses, func(ctx context.Context, b ebdriver.EventBusInfo) (resourcediscovery.DiscoveredResource, error) {
		tags, err := d.p.EventBridge.ListResourceTags(ctx, b.ARN)

		return row("events", "event-bus", b.Name, b.ARN, tags), err
	})
}

func (d taggedDiscovery) discoverACM(ctx context.Context) ([]resourcediscovery.DiscoveredResource, error) {
	certs, err := d.p.ACM.ListCertificates(ctx, acmdriver.ListFilter{})
	if err != nil {
		return nil, err
	}

	return project(ctx, certs, func(_ context.Context, c acmdriver.Certificate) (resourcediscovery.DiscoveredResource, error) {
		return row("acm", "certificate", c.DomainName, c.ARN, c.Tags), nil
	})
}

func (d taggedDiscovery) discoverCloudTrail(ctx context.Context) ([]resourcediscovery.DiscoveredResource, error) {
	trails, err := d.p.CloudTrail.DescribeTrails(ctx, nil)
	if err != nil {
		return nil, err
	}

	return project(ctx, trails, func(ctx context.Context, t cloudtraildriver.Trail) (resourcediscovery.DiscoveredResource, error) {
		byARN, err := d.p.CloudTrail.ListTags(ctx, []string{t.TrailARN})

		return row("cloudtrail", "trail", t.Name, t.TrailARN, byARN[t.TrailARN]), err
	})
}

func (d taggedDiscovery) discoverWAFv2(ctx context.Context) ([]resourcediscovery.DiscoveredResource, error) {
	var out []resourcediscovery.DiscoveredResource

	for _, sc := range []string{wafv2driver.ScopeRegional, wafv2driver.ScopeCloudFront} {
		acls, err := d.p.WAFv2.ListWebACLs(ctx, sc)
		if err != nil {
			return nil, err
		}

		for i := range acls {
			out = append(out, row("wafv2", "webacl", acls[i].Name, acls[i].ARN, acls[i].Tags))
		}
	}

	return out, nil
}

func (d taggedDiscovery) discoverGlue(ctx context.Context) ([]resourcediscovery.DiscoveredResource, error) {
	var (
		out   []resourcediscovery.DiscoveredResource
		token string
	)

	for {
		names, next, err := d.p.Glue.ListJobs(ctx, gluedriver.TablePagination{NextToken: token, MaxResults: pageLimit})
		if err != nil {
			return nil, err
		}

		for _, name := range names {
			arn := idgen.AWSARN("glue", d.p.Region, d.p.AccountID, "job/"+name)

			tags, err := d.p.Glue.GetTags(ctx, arn)
			if err != nil {
				return nil, err
			}

			out = append(out, row("glue", "job", name, arn, tags))
		}

		if next == "" {
			return out, nil
		}

		token = next
	}
}

func (d taggedDiscovery) discoverKafka(ctx context.Context) ([]resourcediscovery.DiscoveredResource, error) {
	var (
		out   []resourcediscovery.DiscoveredResource
		token string
	)

	for {
		clusters, next, err := d.p.Kafka.ListClusters(ctx, "", kafkadriver.Page{NextToken: token, MaxResults: pageLimit})
		if err != nil {
			return nil, err
		}

		for i := range clusters {
			tags, err := d.p.Kafka.ListTagsForResource(ctx, clusters[i].ClusterARN)
			if err != nil {
				return nil, err
			}

			out = append(out, row("kafka", "cluster", clusters[i].ClusterName, clusters[i].ClusterARN, tags))
		}

		if next == "" {
			return out, nil
		}

		token = next
	}
}

func (d taggedDiscovery) discoverKinesis(ctx context.Context) ([]resourcediscovery.DiscoveredResource, error) {
	var (
		out   []resourcediscovery.DiscoveredResource
		token string
	)

	for {
		res, err := d.p.Kinesis.ListStreams(ctx, token, "", pageLimit)
		if err != nil {
			return nil, err
		}

		for i := range res.StreamSummaries {
			s := res.StreamSummaries[i]

			tags, err := d.p.Kinesis.ListTagsForResource(ctx, s.StreamARN)
			if err != nil {
				return nil, err
			}

			out = append(out, row("kinesis", "stream", s.StreamName, s.StreamARN, tags))
		}

		if !res.HasMoreStreams || res.NextToken == "" {
			return out, nil
		}

		token = res.NextToken
	}
}

func (d taggedDiscovery) discoverGuardDuty(ctx context.Context) ([]resourcediscovery.DiscoveredResource, error) {
	var (
		out   []resourcediscovery.DiscoveredResource
		token string
	)

	for {
		ids, next, err := d.p.GuardDuty.ListDetectors(ctx, guarddutydriver.Page{NextToken: token, MaxResults: pageLimit})
		if err != nil {
			return nil, err
		}

		for _, id := range ids {
			det, err := d.p.GuardDuty.GetDetector(ctx, id)
			if err != nil {
				return nil, err
			}

			arn := idgen.AWSARN("guardduty", d.p.Region, d.p.AccountID, "detector/"+id)
			out = append(out, row("guardduty", "detector", id, arn, det.Tags))
		}

		if next == "" {
			return out, nil
		}

		token = next
	}
}

func (d taggedDiscovery) discoverConfig(ctx context.Context) ([]resourcediscovery.DiscoveredResource, error) {
	var (
		out   []resourcediscovery.DiscoveredResource
		token string
	)

	for {
		rules, next, err := d.p.Config.DescribeConfigRules(ctx, nil, configservicedriver.Page{NextToken: token, Limit: pageLimit})
		if err != nil {
			return nil, err
		}

		for i := range rules {
			out = append(out, row("config", "config-rule", rules[i].ConfigRuleID, rules[i].ConfigRuleArn, rules[i].Tags))
		}

		if next == "" {
			return out, nil
		}

		token = next
	}
}

// project applies fn to each item, threading the first error, and returns the
// collected rows. It factors out the identical enumerate → read-tags → row loop
// the single-list discoverers share. The projection returns its error last so a
// tag-read failure inside fn short-circuits the walk with that error.
func project[T any](ctx context.Context, items []T,
	fn func(context.Context, T) (resourcediscovery.DiscoveredResource, error),
) ([]resourcediscovery.DiscoveredResource, error) {
	out := make([]resourcediscovery.DiscoveredResource, 0, len(items))

	for i := range items {
		r, err := fn(ctx, items[i])
		if err != nil {
			return nil, err
		}

		out = append(out, r)
	}

	return out, nil
}

// row builds a DiscoveredResource, dropping an empty tag map so a resource with
// no tags surfaces with nil Tags (matching the other walkers).
func row(service, typ, id, arn string, tags map[string]string) resourcediscovery.DiscoveredResource {
	if len(tags) == 0 {
		tags = nil
	}

	return resourcediscovery.DiscoveredResource{Service: service, Type: typ, ID: id, ARN: arn, Tags: tags}
}

// tagsToMap converts a service's own []Tag slice to a map via a per-type
// key/value accessor.
func tagsToMap[T any](tags []T, kv func(T) (string, string)) map[string]string {
	out := make(map[string]string, len(tags))

	for _, t := range tags {
		k, v := kv(t)
		out[k] = v
	}

	return out
}

func ecsKV(t ecsdriver.Tag) (key, value string)   { return t.Key, t.Value }
func ksKV(t ksdriver.Tag) (key, value string)     { return t.Key, t.Value }
func mdbKV(t mdbdriver.Tag) (key, value string)   { return t.Key, t.Value }
func r53rKV(t r53rdriver.Tag) (key, value string) { return t.Key, t.Value }
