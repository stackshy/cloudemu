package aws

import (
	"context"
	"slices"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	acmdriver "github.com/stackshy/cloudemu/v2/services/acm/driver"
	cloudtraildriver "github.com/stackshy/cloudemu/v2/services/cloudtrail/driver"
	cadriver "github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
	configservicedriver "github.com/stackshy/cloudemu/v2/services/configservice/driver"
	ecsdriver "github.com/stackshy/cloudemu/v2/services/ecs/driver"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	gluedriver "github.com/stackshy/cloudemu/v2/services/glue/driver"
	guarddutydriver "github.com/stackshy/cloudemu/v2/services/guardduty/driver"
	kafkadriver "github.com/stackshy/cloudemu/v2/services/kafka/driver"
	ksdriver "github.com/stackshy/cloudemu/v2/services/keyspaces/driver"
	kinesisdriver "github.com/stackshy/cloudemu/v2/services/kinesis/driver"
	kmsdriver "github.com/stackshy/cloudemu/v2/services/kms/driver"
	mdbdriver "github.com/stackshy/cloudemu/v2/services/memorydb/driver"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
	opensearchdriver "github.com/stackshy/cloudemu/v2/services/opensearch/driver"
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
// tag store (but are not reached by the portable-driver discovery walkers)
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
		d.discoverKeyspaces, d.discoverResolver, d.discoverEventBridge, d.discoverACM, d.discoverCodeArtifact,
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

	return project(ctx, domains, d.openSearchRow)
}

func (d taggedDiscovery) openSearchRow(ctx context.Context, dom opensearchdriver.DomainInfo) (resourcediscovery.DiscoveredResource, error) {
	arn := idgen.AWSARN("es", d.p.Region, d.p.AccountID, "domain/"+dom.DomainName)
	tags, err := d.p.OpenSearch.ListTags(ctx, arn)

	return row("es", "domain", dom.DomainName, arn, tags), err
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
	nf := d.p.NetworkFirewall

	collectors := []func() ([]resourcediscovery.DiscoveredResource, error){
		func() ([]resourcediscovery.DiscoveredResource, error) {
			return pageRows(func(token string) ([]nfdriver.Firewall, string, error) {
				return nf.ListFirewalls(ctx, nil, nfdriver.ListOptions{NextToken: token})
			}, func(f *nfdriver.Firewall) resourcediscovery.DiscoveredResource {
				return row("network-firewall", "firewall", f.Name, f.ARN, f.Tags)
			})
		},
		func() ([]resourcediscovery.DiscoveredResource, error) {
			return pageRows(func(token string) ([]nfdriver.FirewallPolicy, string, error) {
				return nf.ListFirewallPolicies(ctx, nfdriver.ListOptions{NextToken: token})
			}, func(p *nfdriver.FirewallPolicy) resourcediscovery.DiscoveredResource {
				return row("network-firewall", "firewall-policy", p.Name, p.ARN, p.Tags)
			})
		},
		func() ([]resourcediscovery.DiscoveredResource, error) {
			return pageRows(func(token string) ([]nfdriver.RuleGroup, string, error) {
				return nf.ListRuleGroups(ctx, nfdriver.RuleGroupFilter{}, nfdriver.ListOptions{NextToken: token})
			}, func(g *nfdriver.RuleGroup) resourcediscovery.DiscoveredResource {
				return row("network-firewall", "rule-group", g.Name, g.ARN, g.Tags)
			})
		},
		func() ([]resourcediscovery.DiscoveredResource, error) {
			return pageRows(func(token string) ([]nfdriver.TLSInspectionConfig, string, error) {
				return nf.ListTLSInspectionConfigurations(ctx, nfdriver.ListOptions{NextToken: token})
			}, func(c *nfdriver.TLSInspectionConfig) resourcediscovery.DiscoveredResource {
				return row("network-firewall", "tls-configuration", c.Name, c.ARN, c.Tags)
			})
		},
		func() ([]resourcediscovery.DiscoveredResource, error) {
			return pageRows(func(token string) ([]nfdriver.VPCEndpointAssociation, string, error) {
				return nf.ListVpcEndpointAssociations(ctx, "", nfdriver.ListOptions{NextToken: token})
			}, func(a *nfdriver.VPCEndpointAssociation) resourcediscovery.DiscoveredResource {
				return row("network-firewall", "vpc-endpoint-association", a.ID, a.ARN, a.Tags)
			})
		},
	}

	var out []resourcediscovery.DiscoveredResource

	for _, collect := range collectors {
		rows, err := collect()
		if err != nil {
			return nil, err
		}

		out = append(out, rows...)
	}

	return out, nil
}

// pageRows drains a NextToken-paginated list call and maps every item to a row.
func pageRows[T any](
	list func(token string) ([]T, string, error), toRow func(*T) resourcediscovery.DiscoveredResource,
) ([]resourcediscovery.DiscoveredResource, error) {
	items, err := allPages(list)
	if err != nil {
		return nil, err
	}

	rows := make([]resourcediscovery.DiscoveredResource, 0, len(items))
	for i := range items {
		rows = append(rows, toRow(&items[i]))
	}

	return rows, nil
}

// allPages drains a NextToken-paginated list call.
func allPages[T any](list func(token string) ([]T, string, error)) ([]T, error) {
	var all []T

	token := ""

	for {
		items, next, err := list(token)
		if err != nil {
			return nil, err
		}

		all = append(all, items...)

		if next == "" {
			return all, nil
		}

		token = next
	}
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

// discoverCodeArtifact lists CodeArtifact domains, repositories and package
// groups. Each carries its tags in the same store the tagger mutates.
func (d taggedDiscovery) discoverCodeArtifact(ctx context.Context) ([]resourcediscovery.DiscoveredResource, error) {
	ca := d.p.CodeArtifact

	domains, _, err := ca.ListDomains(ctx, cadriver.Page{MaxResults: pageLimit})
	if err != nil {
		return nil, err
	}

	out, err := project(ctx, domains, func(_ context.Context, dom *cadriver.Domain) (resourcediscovery.DiscoveredResource, error) {
		return row("codeartifact", "domain", dom.Name, dom.Arn, dom.Tags), nil
	})
	if err != nil {
		return nil, err
	}

	repos, _, err := ca.ListRepositories(ctx, "", cadriver.Page{MaxResults: pageLimit})
	if err != nil {
		return nil, err
	}

	rows, err := project(ctx, repos, func(_ context.Context, r *cadriver.Repository) (resourcediscovery.DiscoveredResource, error) {
		return row("codeartifact", "repository", r.Name, r.Arn, r.Tags), nil
	})
	if err != nil {
		return nil, err
	}

	out = append(out, rows...)

	for _, dom := range domains {
		groups, _, gerr := ca.ListPackageGroups(ctx, cadriver.DomainRef{Domain: dom.Name}, "", cadriver.Page{MaxResults: pageLimit})
		if gerr != nil {
			return nil, gerr
		}

		rows, err = project(ctx, groups, func(ctx context.Context, g *cadriver.PackageGroup) (resourcediscovery.DiscoveredResource, error) {
			tags, terr := ca.ListTagsForResource(ctx, g.Arn)

			return row("codeartifact", "package-group", g.Pattern, g.Arn, tags), terr
		})
		if err != nil {
			return nil, err
		}

		out = append(out, rows...)
	}

	return out, nil
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
	names, err := listAll(func(token string) ([]string, string, error) {
		return d.p.Glue.ListJobs(ctx, gluedriver.TablePagination{NextToken: token, MaxResults: pageLimit})
	})
	if err != nil {
		return nil, err
	}

	return project(ctx, names, d.glueJobRow)
}

// glueJobRow reads a Glue job's tags. GetTags reads an ARN-keyed store and
// returns no error for a job that no longer exists, so GetJob runs first: a job
// deleted after ListJobs then reports NotFound and is skipped, instead of
// surfacing as a row for a resource that is gone.
func (d taggedDiscovery) glueJobRow(ctx context.Context, name string) (resourcediscovery.DiscoveredResource, error) {
	if _, err := d.p.Glue.GetJob(ctx, name); err != nil {
		return resourcediscovery.DiscoveredResource{}, err
	}

	arn := idgen.AWSARN("glue", d.p.Region, d.p.AccountID, "job/"+name)
	tags, err := d.p.Glue.GetTags(ctx, arn)

	return row("glue", "job", name, arn, tags), err
}

func (d taggedDiscovery) discoverKafka(ctx context.Context) ([]resourcediscovery.DiscoveredResource, error) {
	clusters, err := listAll(func(token string) ([]kafkadriver.Cluster, string, error) {
		return d.p.Kafka.ListClusters(ctx, "", kafkadriver.Page{NextToken: token, MaxResults: pageLimit})
	})
	if err != nil {
		return nil, err
	}

	return project(ctx, clusters, d.kafkaRow)
}

//nolint:gocritic // hugeParam: the signature is project's by-value per-item callback.
func (d taggedDiscovery) kafkaRow(ctx context.Context, c kafkadriver.Cluster) (resourcediscovery.DiscoveredResource, error) {
	tags, err := d.p.Kafka.ListTagsForResource(ctx, c.ClusterARN)

	return row("kafka", "cluster", c.ClusterName, c.ClusterARN, tags), err
}

func (d taggedDiscovery) discoverKinesis(ctx context.Context) ([]resourcediscovery.DiscoveredResource, error) {
	streams, err := listAll(func(token string) ([]kinesisdriver.StreamSummary, string, error) {
		res, err := d.p.Kinesis.ListStreams(ctx, token, "", pageLimit)
		if err != nil {
			return nil, "", err
		}

		if !res.HasMoreStreams {
			return res.StreamSummaries, "", nil
		}

		return res.StreamSummaries, res.NextToken, nil
	})
	if err != nil {
		return nil, err
	}

	return project(ctx, streams, d.kinesisRow)
}

//nolint:gocritic // hugeParam: the signature is project's by-value per-item callback.
func (d taggedDiscovery) kinesisRow(ctx context.Context, s kinesisdriver.StreamSummary) (resourcediscovery.DiscoveredResource, error) {
	tags, err := d.p.Kinesis.ListTagsForResource(ctx, s.StreamARN)

	return row("kinesis", "stream", s.StreamName, s.StreamARN, tags), err
}

func (d taggedDiscovery) discoverGuardDuty(ctx context.Context) ([]resourcediscovery.DiscoveredResource, error) {
	ids, err := d.listGuardDutyDetectors(ctx)
	if err != nil {
		return nil, err
	}

	return project(ctx, ids, d.guardDutyRow)
}

func (d taggedDiscovery) listGuardDutyDetectors(ctx context.Context) ([]string, error) {
	return listAll(func(token string) ([]string, string, error) {
		return d.p.GuardDuty.ListDetectors(ctx, guarddutydriver.Page{NextToken: token, MaxResults: pageLimit})
	})
}

// guardDutyRow reads a detector's tags through GetDetector. Like real GuardDuty,
// GetDetector reports a detector that does not exist as BadRequestException,
// not NotFound, so the error alone can't tell a deleted detector from a real
// failure. When GetDetector fails, a fresh ListDetectors decides: if the
// detector is no longer listed it was deleted after the walk listed it, and the
// row is reported as NotFound so project skips it. Otherwise the original error
// propagates.
func (d taggedDiscovery) guardDutyRow(ctx context.Context, id string) (resourcediscovery.DiscoveredResource, error) {
	det, err := d.p.GuardDuty.GetDetector(ctx, id)
	if err != nil {
		if ids, lerr := d.listGuardDutyDetectors(ctx); lerr == nil && !slices.Contains(ids, id) {
			return resourcediscovery.DiscoveredResource{}, cerrors.Newf(cerrors.NotFound, "detector %s was deleted", id)
		}

		return resourcediscovery.DiscoveredResource{}, err
	}

	arn := idgen.AWSARN("guardduty", d.p.Region, d.p.AccountID, "detector/"+id)

	return row("guardduty", "detector", id, arn, det.Tags), nil
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
//
// A NotFound from fn means the resource was deleted between the list call and
// its tag read. It is skipped, the same as a resource that was never listed,
// so a delete racing GetResources doesn't fail the whole inventory (the
// resourcediscovery walkers handle this race the same way). Any other error
// still propagates.
func project[T any](ctx context.Context, items []T,
	fn func(context.Context, T) (resourcediscovery.DiscoveredResource, error),
) ([]resourcediscovery.DiscoveredResource, error) {
	out := make([]resourcediscovery.DiscoveredResource, 0, len(items))

	for i := range items {
		r, err := fn(ctx, items[i])
		if cerrors.IsNotFound(err) {
			continue
		}

		if err != nil {
			return nil, err
		}

		out = append(out, r)
	}

	return out, nil
}

// listAll follows a NextToken-paginated list call to its last page and returns
// every item, so a paginated walker can hand the full list to project.
func listAll[T any](list func(token string) ([]T, string, error)) ([]T, error) {
	var (
		all   []T
		token string
	)

	for {
		items, next, err := list(token)
		if err != nil {
			return nil, err
		}

		all = append(all, items...)

		if next == "" {
			return all, nil
		}

		token = next
	}
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
