package resourcediscovery

import (
	"context"
	"fmt"
)

// Provider name constants used for routing per-provider ARN construction.
const (
	ProviderAWS   = "aws"
	ProviderAzure = "azure"
	ProviderGCP   = "gcp"
)

// Service name constants embedded in Resource.Service. These are the
// portable-API service identifiers, not provider-specific names. Callers
// translate to per-provider service names at the SDK boundary.
const (
	ServiceCompute    = "compute"
	ServiceNetworking = "networking"
	ServiceStorage    = "storage"
	ServiceDatabase   = "database"
	ServiceServerless = "serverless"
)

// Resource type constants emitted by the walkers.
const (
	TypeInstance      = "Instance"
	TypeVPC           = "VPC"
	TypeSubnet        = "Subnet"
	TypeSecurityGroup = "SecurityGroup"
	TypeBucket        = "Bucket"
	TypeTable         = "Table"
	TypeFunction      = "Function"
)

func (e *Engine) walkCompute(ctx context.Context) ([]Resource, error) {
	instances, err := e.drivers.Compute.DescribeInstances(ctx, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("walkCompute: %w", err)
	}

	out := make([]Resource, 0, len(instances))
	for i := range instances {
		out = append(out, Resource{
			Provider: e.provider,
			Service:  ServiceCompute,
			Type:     TypeInstance,
			ID:       instances[i].ID,
			ARN:      e.computeInstanceARN(instances[i].ID),
			Region:   e.region,
			Tags:     copyTags(instances[i].Tags),
		})
	}

	return out, nil
}

func (e *Engine) walkNetworking(ctx context.Context) ([]Resource, error) {
	out := []Resource{}

	vpcs, err := e.drivers.Networking.DescribeVPCs(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("walkNetworking vpcs: %w", err)
	}

	for _, v := range vpcs {
		out = append(out, Resource{
			Provider: e.provider, Service: ServiceNetworking, Type: TypeVPC,
			ID:     v.ID,
			ARN:    e.networkARN(netKindVPC, v.ID),
			Region: e.region, Tags: copyTags(v.Tags),
		})
	}

	subnets, err := e.drivers.Networking.DescribeSubnets(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("walkNetworking subnets: %w", err)
	}

	for _, s := range subnets {
		out = append(out, Resource{
			Provider: e.provider, Service: ServiceNetworking, Type: TypeSubnet,
			ID:     s.ID,
			ARN:    e.networkARN(netKindSubnet, s.ID),
			Region: e.region, Tags: copyTags(s.Tags),
		})
	}

	sgs, err := e.drivers.Networking.DescribeSecurityGroups(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("walkNetworking sgs: %w", err)
	}

	for _, sg := range sgs {
		out = append(out, Resource{
			Provider: e.provider, Service: ServiceNetworking, Type: TypeSecurityGroup,
			ID:     sg.ID,
			ARN:    e.networkARN(netKindSecurityGroup, sg.ID),
			Region: e.region, Tags: copyTags(sg.Tags),
		})
	}

	return out, nil
}

func (e *Engine) walkStorage(ctx context.Context) ([]Resource, error) {
	buckets, err := e.drivers.Storage.ListBuckets(ctx)
	if err != nil {
		return nil, fmt.Errorf("walkStorage: %w", err)
	}

	out := make([]Resource, 0, len(buckets))

	for _, b := range buckets {
		tags, tagErr := e.drivers.Storage.GetBucketTagging(ctx, b.Name)
		if tagErr != nil {
			// Buckets without tags surface a NotFound from the tagging API
			// in real S3 — treat as "no tags" rather than failing the walk.
			tags = nil
		}

		region := b.Region
		if region == "" {
			region = e.region
		}

		out = append(out, Resource{
			Provider: e.provider, Service: ServiceStorage, Type: TypeBucket,
			ID:     b.Name,
			ARN:    e.storageBucketARN(b.Name),
			Region: region, Tags: tags,
		})
	}

	return out, nil
}

func (e *Engine) walkDatabase(ctx context.Context) ([]Resource, error) {
	tables, err := e.drivers.Database.ListTables(ctx)
	if err != nil {
		return nil, fmt.Errorf("walkDatabase: %w", err)
	}

	out := make([]Resource, 0, len(tables))

	for _, name := range tables {
		tags, tagErr := e.drivers.Database.ListTagsOfResource(ctx, name)
		if tagErr != nil {
			tags = nil
		}

		out = append(out, Resource{
			Provider: e.provider, Service: ServiceDatabase, Type: TypeTable,
			ID:     name,
			ARN:    e.databaseTableARN(name),
			Region: e.region, Tags: tags,
		})
	}

	return out, nil
}

func (e *Engine) walkServerless(ctx context.Context) ([]Resource, error) {
	fns, err := e.drivers.Serverless.ListFunctions(ctx)
	if err != nil {
		return nil, fmt.Errorf("walkServerless: %w", err)
	}

	out := make([]Resource, 0, len(fns))

	for i := range fns {
		// FunctionInfo carries a populated ARN — use it directly rather than
		// re-deriving, so the value matches what the function's own service
		// returned.
		arn := fns[i].ARN
		if arn == "" {
			arn = e.serverlessFunctionARN(fns[i].Name)
		}

		out = append(out, Resource{
			Provider: e.provider, Service: ServiceServerless, Type: TypeFunction,
			ID:     fns[i].Name,
			ARN:    arn,
			Region: e.region, Tags: copyTags(fns[i].Tags),
		})
	}

	return out, nil
}

func copyTags(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}

	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}

	return out
}
