package resourcediscovery

import (
	"context"
	"fmt"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
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
	ServiceCompute      = "compute"
	ServiceNetworking   = "networking"
	ServiceStorage      = "storage"
	ServiceDatabase     = "database"
	ServiceServerless   = "serverless"
	ServiceDatabricks   = "databricks"
	ServiceKubernetes   = "kubernetes"
	ServiceRelationalDB = "relationaldb"
)

// Resource type constants emitted by the walkers.
const (
	TypeInstance      = "Instance"
	TypeVolume        = "Volume"
	TypeVPC           = "VPC"
	TypeSubnet        = "Subnet"
	TypeSecurityGroup = "SecurityGroup"
	TypeNetworkIface  = "NetworkInterface"
	TypeElasticIP     = "ElasticIP"
	TypeBucket        = "Bucket"
	TypeTable         = "Table"
	TypeFunction      = "Function"
	TypeWorkspace     = "Workspace"
	TypeCluster       = "Cluster"
	TypeNodeGroup     = "NodeGroup"
	TypeDBInstance    = "DBInstance"
	TypeDBCluster     = "DBCluster"
	TypeDBSnapshot    = "DBSnapshot"
)

// Azure/GCP managed-SQL server types. These portable types map to per-cloud
// native type strings in Resource Graph (Azure) and Cloud Asset (GCP). AWS RDS
// uses TypeDBInstance/DBCluster/DBSnapshot above.
const (
	TypeSQLServer       = "SqlServer"              // Azure SQL logical server
	TypeMySQLFlex       = "MySqlFlexibleServer"    // Azure Database for MySQL Flexible Server
	TypePostgresFlex    = "PostgresFlexibleServer" // Azure Database for PostgreSQL Flexible Server
	TypeSQLInstance     = "SqlInstance"            // GCP Cloud SQL instance
	TypeManagedInstance = "SqlManagedInstance"     // Azure SQL Managed Instance
	TypeAlloyDBCluster  = "AlloyDBCluster"         // GCP AlloyDB cluster
)

func (e *Engine) walkCompute(ctx context.Context) ([]Resource, error) {
	// Resource discovery is an internal/system walk: managed (service-owned)
	// instances still exist and must be discoverable even when the account
	// hides them from the public Describe API, so opt in explicitly.
	instances, err := e.drivers.Compute.DescribeInstances(ctx, nil, nil,
		computedriver.DescribeInstancesOptions{IncludeManagedResources: true})
	if err != nil {
		return nil, fmt.Errorf("walkCompute: %w", err)
	}

	out := make([]Resource, 0, len(instances))

	for i := range instances {
		inst := &instances[i]

		props := map[string]any{}
		putStr(props, "osType", inst.OSType)
		putStr(props, "priority", inst.Priority)
		putStr(props, "licenseType", inst.LicenseType)

		out = append(out, Resource{
			Provider:   e.provider,
			Service:    ServiceCompute,
			Type:       TypeInstance,
			ID:         inst.ID,
			ARN:        e.computeInstanceARN(inst.ID),
			Region:     e.region,
			Tags:       copyTags(inst.Tags),
			SKU:        inst.InstanceType,
			Zones:      cloneStrings(inst.Zones),
			Properties: orNilProps(props),
		})
	}

	vols, err := e.walkVolumes(ctx)
	if err != nil {
		return nil, err
	}

	return append(out, vols...), nil
}

// walkVolumes surfaces block volumes (EBS / Azure managed disks / GCE PDs) as
// first-class resources, so a discoverer sees them the way the real cloud APIs
// do. ManagedBy links the volume to its owning instance.
func (e *Engine) walkVolumes(ctx context.Context) ([]Resource, error) {
	vols, err := e.drivers.Compute.DescribeVolumes(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("walkCompute volumes: %w", err)
	}

	out := make([]Resource, 0, len(vols))

	for i := range vols {
		v := &vols[i]

		props := map[string]any{"diskSizeGB": v.Size}
		putInt(props, "diskIOPSReadWrite", v.IOPS)
		putInt(props, "diskMBpsReadWrite", v.Throughput)
		putStr(props, "diskState", v.State)

		managedBy := ""
		if v.AttachedTo != "" {
			managedBy = e.computeInstanceARN(v.AttachedTo)
		}

		out = append(out, Resource{
			Provider:   e.provider,
			Service:    ServiceCompute,
			Type:       TypeVolume,
			ID:         shortName(v.ID),
			ARN:        e.computeVolumeARN(v.ID),
			Region:     e.region,
			Tags:       copyTags(v.Tags),
			SKU:        v.VolumeType,
			ManagedBy:  managedBy,
			Zones:      zonesOf(v.AvailabilityZone),
			Properties: props,
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

	eips, err := e.drivers.Networking.DescribeAddresses(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("walkNetworking addresses: %w", err)
	}

	for _, eip := range eips {
		out = append(out, Resource{
			Provider: e.provider, Service: ServiceNetworking, Type: TypeElasticIP,
			ID:     eip.AllocationID,
			ARN:    e.networkARN(netKindElasticIP, eip.AllocationID),
			Region: e.region, Tags: copyTags(eip.Tags),
		})
	}

	ifaces, err := e.walkNetworkInterfaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("walkNetworking interfaces: %w", err)
	}

	return append(out, ifaces...), nil
}

// walkNetworkInterfaces adds interfaces when the driver models them.
//
// They are an optional capability, so a driver without them contributes
// nothing rather than failing the whole walk — a cloud that has no interfaces
// has none to discover, which is not an error.
func (e *Engine) walkNetworkInterfaces(ctx context.Context) ([]Resource, error) {
	enisDriver, ok := e.drivers.Networking.(netdriver.NetworkInterfaces)
	if !ok {
		return nil, nil
	}

	// A driver that models interfaces and then fails to list them has a real
	// problem, and swallowing it would report a complete inventory that is
	// missing whatever the walk could not read.
	enis, err := enisDriver.DescribeNetworkInterfaces(ctx, nil)
	if err != nil {
		return nil, err
	}

	out := make([]Resource, 0, len(enis))

	for _, eni := range enis {
		out = append(out, Resource{
			Provider: e.provider, Service: ServiceNetworking, Type: TypeNetworkIface,
			ID:     eni.ID,
			ARN:    e.networkARN(netKindNetworkIface, eni.ID),
			Region: e.region, Tags: copyTags(eni.Tags),
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
			// NotFound means the bucket was deleted between ListBuckets and
			// this tagging lookup. Skip it from the inventory; any other
			// error is real and must propagate.
			if cerrors.IsNotFound(tagErr) {
				continue
			}

			return nil, fmt.Errorf("walkStorage tags %q: %w", b.Name, tagErr)
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
			// Race window: table was deleted between ListTables and the
			// tagging lookup. Skip the now-gone resource. Any other error
			// is real and must propagate.
			if cerrors.IsNotFound(tagErr) {
				continue
			}

			return nil, fmt.Errorf("walkDatabase tags %q: %w", name, tagErr)
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

// walkDatabricks lists Databricks workspaces from the control-plane driver.
// Workspaces are ARM resources whose driver is wired in separately from the
// five portable service drivers, so they need their own walker to appear in
// the cross-service inventory (and therefore in Resource Graph results).
func (e *Engine) walkDatabricks(ctx context.Context) ([]Resource, error) {
	workspaces, err := e.drivers.Databricks.ListWorkspaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("walkDatabricks: %w", err)
	}

	out := make([]Resource, 0, len(workspaces))

	for i := range workspaces {
		region := workspaces[i].Location
		if region == "" {
			region = e.region
		}

		out = append(out, Resource{
			Provider: e.provider, Service: ServiceDatabricks, Type: TypeWorkspace,
			ID:     workspaces[i].Name,
			ARN:    workspaces[i].ID,
			Region: region, Tags: copyTags(workspaces[i].Tags),
		})
	}

	return out, nil
}

// walkKubernetes surfaces managed Kubernetes clusters (EKS/GKE/AKS) and their
// node groups. The provider supplies a DiscoverClusters adapter; each cluster
// becomes a Cluster resource and each node group / node pool / agent pool a
// NodeGroup resource, so a client enumerating inventory sees them the way real
// RE2 / Resource Graph / Cloud Asset would.
func (e *Engine) walkKubernetes(ctx context.Context) ([]Resource, error) {
	clusters, err := e.drivers.Kubernetes.DiscoverClusters(ctx)
	if err != nil {
		return nil, fmt.Errorf("walkKubernetes: %w", err)
	}

	out := []Resource{}

	for i := range clusters {
		c := clusters[i]

		region := c.Region
		if region == "" {
			region = e.region
		}

		clusterARN := c.ARN
		if clusterARN == "" {
			clusterARN = e.kubernetesClusterARN(region, c.ResourceGroup, c.Name)
		}

		cluster := Resource{
			Provider: e.provider, Service: ServiceKubernetes, Type: TypeCluster,
			ID:     c.Name,
			ARN:    clusterARN,
			Region: region, Tags: copyTags(c.Tags),
		}
		applyAttrs(&cluster, &c.Attrs)
		out = append(out, cluster)

		for _, ng := range c.NodeGroups {
			out = append(out, Resource{
				Provider: e.provider, Service: ServiceKubernetes, Type: TypeNodeGroup,
				ID:     ng,
				ARN:    e.kubernetesNodeGroupARN(region, c.ResourceGroup, c.Name, ng),
				Region: region,
			})
		}
	}

	return out, nil
}

// walkRelationalDB surfaces managed relational database servers (RDS/Aurora,
// Azure SQL, Azure MySQL/PostgreSQL Flexible Server, Cloud SQL). The provider
// supplies a DiscoverDatabases adapter; each server becomes a relational-db
// resource whose Type carries the per-cloud kind so Resource Explorer /
// Resource Graph / Cloud Asset can translate it to the native type string.
func (e *Engine) walkRelationalDB(ctx context.Context) ([]Resource, error) {
	dbs, err := e.drivers.RelationalDB.DiscoverDatabases(ctx)
	if err != nil {
		return nil, fmt.Errorf("walkRelationalDB: %w", err)
	}

	out := []Resource{}

	for i := range dbs {
		d := dbs[i]

		region := d.Region
		if region == "" {
			region = e.region
		}

		typ := d.Type
		if typ == "" {
			typ = TypeDBInstance
		}

		r := Resource{
			Provider: e.provider, Service: ServiceRelationalDB, Type: typ,
			ID:     d.Name,
			ARN:    d.ARN,
			Region: region, Tags: copyTags(d.Tags),
		}
		applyAttrs(&r, &d.Attrs)
		out = append(out, r)
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

// applyAttrs copies the generic attribute slots from a Discovered* projection
// onto the emitted Resource. Kept in one place so every walker fills the slots
// identically, with no per-type branching.
func applyAttrs(r *Resource, a *Attributes) {
	r.SKU = a.SKU
	r.Kind = a.Kind
	r.ManagedBy = a.ManagedBy
	r.Zones = cloneStrings(a.Zones)
	r.Properties = cloneProps(a.Properties)
}

func cloneStrings(s []string) []string {
	if len(s) == 0 {
		return nil
	}

	return append([]string(nil), s...)
}

func cloneProps(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}

	out := make(map[string]any, len(src))
	for k, v := range src {
		out[k] = v
	}

	return out
}

// orNilProps returns nil for an empty map so an empty Properties bag is omitted
// downstream rather than rendered as an empty object.
func orNilProps(m map[string]any) map[string]any {
	if len(m) == 0 {
		return nil
	}

	return m
}

func putStr(m map[string]any, key, val string) {
	if val != "" {
		m[key] = val
	}
}

func putInt(m map[string]any, key string, val int) {
	if val > 0 {
		m[key] = val
	}
}

func zonesOf(zone string) []string {
	if zone == "" {
		return nil
	}

	return []string{zone}
}

// shortName returns the last path segment of an id (the resource's short name),
// or the id unchanged when it has no separator.
func shortName(id string) string {
	for i := len(id) - 1; i >= 0; i-- {
		if id[i] == '/' {
			return id[i+1:]
		}
	}

	return id
}
