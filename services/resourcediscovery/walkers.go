package resourcediscovery

import (
	"context"
	"fmt"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// Provider name constants used for routing per-provider ARN construction.
const (
	ProviderAWS   = "aws"
	ProviderAzure = "azure"
	ProviderGCP   = "gcp"
	ProviderOCI   = "oci"
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
	// ServiceAppService buckets App Service plans (Azure serverfarms). They are
	// not serverless — they carry a provisioned SKU/tier — so they get their own
	// discriminator rather than sharing ServiceServerless with Functions.
	ServiceAppService = "appservice"
	ServiceSecrets    = "secrets"
	ServiceContainer  = "containerregistry"
	ServiceQueue      = "messagequeue"
	ServiceNotif      = "notification"
	ServiceDNS        = "dns"
	ServiceLogging    = "logging"
	ServiceCache      = "cache"
	ServiceLB         = "loadbalancer"
	ServiceMonitoring = "monitoring"
	ServiceIAM        = "iam"
	ServiceRedshift   = "redshift"
	ServiceSageMaker  = "sagemaker"
	ServiceVertexAI   = "aiplatform"
	ServiceAzureML    = "machinelearningservices"
	ServiceCognitive  = "cognitiveservices"
)

// Resource type constants emitted by the walkers.
const (
	TypeInstance          = "Instance"
	TypeVolume            = "Volume"
	TypeSnapshot          = "Snapshot"
	TypeVPC               = "VPC"
	TypeSubnet            = "Subnet"
	TypeSecurityGroup     = "SecurityGroup"
	TypeNetworkIface      = "NetworkInterface"
	TypeElasticIP         = "ElasticIP"
	TypeBucket            = "Bucket"
	TypeTable             = "Table"
	TypeFunction          = "Function"
	TypeWorkspace         = "Workspace"
	TypeCluster           = "Cluster"
	TypeNodeGroup         = "NodeGroup"
	TypeDBInstance        = "DBInstance"
	TypeDBCluster         = "DBCluster"
	TypeDBSnapshot        = "DBSnapshot"
	TypeDBProxy           = "DBProxy"
	TypeScaleSet          = "ScaleSet"
	TypeAppServicePlan    = "AppServicePlan"
	TypeSecret            = "Secret"
	TypeRepository        = "Repository"
	TypeQueue             = "Queue"
	TypeTopic             = "Topic"
	TypeZone              = "Zone"
	TypeLogGroup          = "LogGroup"
	TypeCacheCluster      = "CacheCluster"
	TypeLoadBalancer      = "LoadBalancer"
	TypeAlarm             = "Alarm"
	TypeUser              = "User"
	TypeRole              = "Role"
	TypePolicy            = "Policy"
	TypeGroup             = "Group"
	TypeNATGateway        = "NatGateway"
	TypeInternetGateway   = "InternetGateway"
	TypePeeringConnection = "PeeringConnection"
	TypeRouteTable        = "RouteTable"
	TypeModel             = "Model"
	TypeEndpoint          = "Endpoint"
	TypeNotebookInstance  = "NotebookInstance"
	TypeDataset           = "Dataset"
	TypeAccount           = "Account"
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
	TypeSQLDatabase     = "SqlDatabase"            // Azure SQL logical database
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
		putStr(props, "priority", inst.Priority)
		putStr(props, "licenseType", inst.LicenseType)
		// osType nests under storageProfile.osDisk to match the real Azure ARG
		// VM shape (a discoverer reads it there). Only Azure VMs set OSType — the
		// AWS/GCP compute mocks leave it empty, so no Azure shape leaks onto them.
		if inst.OSType != "" {
			props["storageProfile"] = map[string]any{"osDisk": map[string]any{"osType": inst.OSType}}
		}

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

	out = append(out, vols...)

	snaps, err := e.walkSnapshots(ctx)
	if err != nil {
		return nil, err
	}

	return append(out, snaps...), nil
}

// walkSnapshots surfaces block-storage snapshots (EBS / GCE / Azure disk
// snapshots) so they appear in the inventory/search APIs.
func (e *Engine) walkSnapshots(ctx context.Context) ([]Resource, error) {
	snaps, err := e.drivers.Compute.DescribeSnapshots(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("walkCompute snapshots: %w", err)
	}

	return e.emitSimple(ServiceCompute, TypeSnapshot, len(snaps),
		func(i int) (string, string, map[string]string) {
			return shortName(snaps[i].ID), e.computeSnapshotARN(snaps[i].ID), snaps[i].Tags
		}), nil
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
		putStr(props, "tier", v.Tier)

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
			SKUTier:    v.Tier,
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
		var props map[string]any
		if v.CIDRBlock != "" {
			props = map[string]any{"addressSpace": map[string]any{"addressPrefixes": []string{v.CIDRBlock}}}
		}

		out = append(out, Resource{
			Provider: e.provider, Service: ServiceNetworking, Type: TypeVPC,
			ID:     v.ID,
			ARN:    e.networkARN(netKindVPC, v.ID),
			Region: e.region, Tags: copyTags(v.Tags),
			Properties: props,
		})
	}

	subnets, err := e.drivers.Networking.DescribeSubnets(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("walkNetworking subnets: %w", err)
	}

	for _, s := range subnets {
		var props map[string]any
		if s.CIDRBlock != "" {
			props = map[string]any{"addressPrefix": s.CIDRBlock}
		}

		out = append(out, Resource{
			Provider: e.provider, Service: ServiceNetworking, Type: TypeSubnet,
			ID:     s.ID,
			ARN:    e.networkARN(netKindSubnet, s.ID),
			Region: e.region, Tags: copyTags(s.Tags),
			Properties: props,
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
		var props map[string]any
		if eip.AllocationMethod != "" {
			props = map[string]any{"publicIPAllocationMethod": eip.AllocationMethod}
		}

		out = append(out, Resource{
			Provider: e.provider, Service: ServiceNetworking, Type: TypeElasticIP,
			ID:     eip.AllocationID,
			ARN:    e.networkARN(netKindElasticIP, eip.AllocationID),
			Region: e.region, Tags: copyTags(eip.Tags),
			SKU:        eip.SKU,
			Properties: props,
		})
	}

	natgws, err := e.drivers.Networking.DescribeNATGateways(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("walkNetworking nat gateways: %w", err)
	}

	for _, ng := range natgws {
		out = append(out, Resource{
			Provider: e.provider, Service: ServiceNetworking, Type: TypeNATGateway,
			ID:     ng.ID,
			ARN:    e.networkARN(netKindNATGateway, ng.ID),
			Region: e.region, Tags: copyTags(ng.Tags),
		})
	}

	igws, err := e.drivers.Networking.DescribeInternetGateways(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("walkNetworking internet gateways: %w", err)
	}

	for _, igw := range igws {
		out = append(out, Resource{
			Provider: e.provider, Service: ServiceNetworking, Type: TypeInternetGateway,
			ID:     igw.ID,
			ARN:    e.networkARN(netKindInternetGW, igw.ID),
			Region: e.region, Tags: copyTags(igw.Tags),
		})
	}

	peerings, err := e.drivers.Networking.DescribePeeringConnections(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("walkNetworking peering connections: %w", err)
	}

	for _, pc := range peerings {
		out = append(out, Resource{
			Provider: e.provider, Service: ServiceNetworking, Type: TypePeeringConnection,
			ID:     pc.ID,
			ARN:    e.networkARN(netKindPeering, pc.ID),
			Region: e.region, Tags: copyTags(pc.Tags),
		})
	}

	rts, err := e.drivers.Networking.DescribeRouteTables(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("walkNetworking route tables: %w", err)
	}

	for _, rt := range rts {
		out = append(out, Resource{
			Provider: e.provider, Service: ServiceNetworking, Type: TypeRouteTable,
			ID:     rt.ID,
			ARN:    e.networkARN(netKindRouteTable, rt.ID),
			Region: e.region, Tags: copyTags(rt.Tags),
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

		res := Resource{
			Provider: e.provider, Service: ServiceStorage, Type: TypeBucket,
			ID:     b.Name,
			ARN:    e.storageBucketARN(b.Name),
			Region: region, Tags: tags,
		}

		// Optional capability: providers whose buckets carry storage-account
		// attributes (Azure) project SKU/kind/access-tier for cost discovery.
		// A non-nil error here is load-bearing: silently dropping it would leave
		// the cost fields absent — the exact failure this projection closes — so
		// propagate it rather than swallow.
		if attrer, ok := e.drivers.Storage.(storagedriver.BucketAttributes); ok {
			a, aErr := attrer.BucketAttributes(ctx, b.Name)
			if aErr != nil {
				return nil, fmt.Errorf("walkStorage attributes %q: %w", b.Name, aErr)
			}

			res.SKU = a.SKU
			res.Kind = a.Kind

			if a.AccessTier != "" {
				res.Properties = map[string]any{"accessTier": a.AccessTier}
			}
		}

		out = append(out, res)
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

		res := Resource{
			Provider: e.provider, Service: ServiceDatabase, Type: TypeTable,
			ID:     name,
			ARN:    e.databaseTableARN(name),
			Region: e.region, Tags: tags,
		}

		// Optional capability: providers whose tables map to a richer account
		// resource (Azure Cosmos DB) project the account's cost attributes.
		// A non-nil error here is load-bearing: silently dropping it would leave
		// the cost attributes absent — the exact failure this projection closes —
		// so propagate it rather than swallow.
		if attrer, ok := e.drivers.Database.(dbdriver.TableAttributes); ok {
			a, aErr := attrer.TableAttributes(ctx, name)
			if aErr != nil {
				return nil, fmt.Errorf("walkDatabase attributes %q: %w", name, aErr)
			}

			res.Kind = a.Kind

			props := map[string]any{}
			putStr(props, "databaseAccountOfferType", a.OfferType)

			if len(a.Capabilities) > 0 {
				caps := make([]any, 0, len(a.Capabilities))
				for _, c := range a.Capabilities {
					caps = append(caps, map[string]any{"name": c})
				}

				props["capabilities"] = caps
			}

			if a.EnableFreeTier {
				props["enableFreeTier"] = true
			}

			res.Properties = orNilProps(props)
		}

		out = append(out, res)
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

		w := &workspaces[i]

		props := map[string]any{}
		putStr(props, "workspaceId", w.WorkspaceID)
		putStr(props, "provisioningState", w.ProvisioningState)

		out = append(out, Resource{
			Provider: e.provider, Service: ServiceDatabricks, Type: TypeWorkspace,
			ID:     w.Name,
			ARN:    w.ID,
			Region: region, Tags: copyTags(w.Tags),
			SKU:        w.SKUName,
			SKUTier:    w.SKUTier,
			Properties: orNilProps(props),
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

		for j := range c.NodeGroups {
			ng := &c.NodeGroups[j]

			pool := Resource{
				Provider: e.provider, Service: ServiceKubernetes, Type: TypeNodeGroup,
				ID:     ng.Name,
				ARN:    e.kubernetesNodeGroupARN(region, c.ResourceGroup, c.Name, ng.Name),
				Region: region,
			}
			applyAttrs(&pool, &ng.Attrs)
			out = append(out, pool)
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

		svc := d.Service
		if svc == "" {
			svc = ServiceRelationalDB
		}

		r := Resource{
			Provider: e.provider, Service: svc, Type: typ,
			ID:     d.Name,
			ARN:    d.ARN,
			Region: region, Tags: copyTags(d.Tags),
		}
		applyAttrs(&r, &d.Attrs)
		out = append(out, r)
	}

	return out, nil
}

// walkVMSS surfaces VM scale sets (Azure VMSS) from the ScaleSets discovery
// adapter, each with its SKU (name/tier/capacity) and nested
// virtualMachineProfile properties.
func (e *Engine) walkVMSS(ctx context.Context) ([]Resource, error) {
	sets, err := e.drivers.ScaleSets.DiscoverScaleSets(ctx)
	if err != nil {
		return nil, fmt.Errorf("walkVMSS: %w", err)
	}

	out := make([]Resource, 0, len(sets))

	for i := range sets {
		s := sets[i]

		region := s.Region
		if region == "" {
			region = e.region
		}

		r := Resource{
			Provider: e.provider, Service: ServiceCompute, Type: TypeScaleSet,
			ID:     s.Name,
			ARN:    s.ARN,
			Region: region, Tags: copyTags(s.Tags),
		}
		applyAttrs(&r, &s.Attrs)
		out = append(out, r)
	}

	return out, nil
}

// walkAppServicePlans surfaces App Service plans (Azure serverfarms) from the
// AppServicePlans discovery adapter, each carrying its pricing tier as sku.
func (e *Engine) walkAppServicePlans(ctx context.Context) ([]Resource, error) {
	plans, err := e.drivers.AppServicePlans.DiscoverAppServicePlans(ctx)
	if err != nil {
		return nil, fmt.Errorf("walkAppServicePlans: %w", err)
	}

	out := make([]Resource, 0, len(plans))

	for i := range plans {
		p := plans[i]

		region := p.Region
		if region == "" {
			region = e.region
		}

		r := Resource{
			Provider: e.provider, Service: ServiceAppService, Type: TypeAppServicePlan,
			ID:     p.Name,
			ARN:    p.ARN,
			Region: region, Tags: copyTags(p.Tags),
		}
		applyAttrs(&r, &p.Attrs)
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
	r.SKUTier = a.SKUTier
	r.SKUCapacity = a.SKUCapacity
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

// emitSimple builds one Resource per item for the flat, region-agnostic
// resource kinds (secrets, repositories, queues, topics, …) whose walkers
// differ only by which driver they list and how they project an item onto an
// (id, arn, tags) triple. Empty arn falls back to id. Keeping the boilerplate
// here lets each walker be a one-line list + projection.
func (e *Engine) emitSimple(
	service, typ string, n int, at func(i int) (id, arn string, tags map[string]string),
) []Resource {
	out := make([]Resource, 0, n)

	for i := range n {
		id, arn, tags := at(i)
		if arn == "" {
			arn = id
		}

		out = append(out, Resource{
			Provider: e.provider, Service: service, Type: typ,
			ID:     id,
			ARN:    arn,
			Region: e.region,
			Tags:   tags,
		})
	}

	return out
}

// walkSecrets surfaces managed secrets (Secrets Manager / Secret Manager / Key
// Vault) so they appear in the inventory/search APIs.
func (e *Engine) walkSecrets(ctx context.Context) ([]Resource, error) {
	secrets, err := e.drivers.Secrets.ListSecrets(ctx)
	if err != nil {
		return nil, fmt.Errorf("walkSecrets: %w", err)
	}

	return e.emitSimple(ServiceSecrets, TypeSecret, len(secrets),
		func(i int) (string, string, map[string]string) {
			return secrets[i].Name, secrets[i].ResourceID, secrets[i].Tags
		}), nil
}

// walkContainerRegistry surfaces container repositories (ECR / Artifact Registry
// / ACR) so they appear in the inventory/search APIs.
func (e *Engine) walkContainerRegistry(ctx context.Context) ([]Resource, error) {
	repos, err := e.drivers.ContainerReg.ListRepositories(ctx)
	if err != nil {
		return nil, fmt.Errorf("walkContainerRegistry: %w", err)
	}

	return e.emitSimple(ServiceContainer, TypeRepository, len(repos),
		func(i int) (string, string, map[string]string) {
			return repos[i].Name, repos[i].URI, repos[i].Tags
		}), nil
}

// walkMessageQueue surfaces message queues (SQS / Service Bus / Pub-Sub) so they
// appear in the inventory/search APIs.
func (e *Engine) walkMessageQueue(ctx context.Context) ([]Resource, error) {
	queues, err := e.drivers.MessageQueue.ListQueues(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("walkMessageQueue: %w", err)
	}

	return e.emitSimple(ServiceQueue, TypeQueue, len(queues),
		func(i int) (string, string, map[string]string) {
			arn := queues[i].ARN
			if arn == "" {
				arn = queues[i].URL
			}

			return queues[i].Name, arn, queues[i].Tags
		}), nil
}

// walkNotification surfaces notification topics (SNS / FCM / Notification Hubs)
// so they appear in the inventory/search APIs.
func (e *Engine) walkNotification(ctx context.Context) ([]Resource, error) {
	topics, err := e.drivers.Notification.ListTopics(ctx, scope.Scope{})
	if err != nil {
		return nil, fmt.Errorf("walkNotification: %w", err)
	}

	return e.emitSimple(ServiceNotif, TypeTopic, len(topics),
		func(i int) (string, string, map[string]string) {
			arn := topics[i].ResourceID
			if arn == "" {
				arn = topics[i].ID
			}

			return topics[i].Name, arn, topics[i].Tags
		}), nil
}

// walkDNS surfaces DNS hosted zones (Route 53 / Azure DNS / Cloud DNS) so they
// appear in the inventory/search APIs.
func (e *Engine) walkDNS(ctx context.Context) ([]Resource, error) {
	zones, err := e.drivers.DNS.ListZones(ctx, scope.Scope{})
	if err != nil {
		return nil, fmt.Errorf("walkDNS: %w", err)
	}

	return e.emitSimple(ServiceDNS, TypeZone, len(zones),
		func(i int) (string, string, map[string]string) {
			return zones[i].Name, zones[i].ID, zones[i].Tags
		}), nil
}

// walkLogging surfaces log groups (CloudWatch Logs / Cloud Logging / Log
// Analytics) so they appear in the inventory/search APIs.
func (e *Engine) walkLogging(ctx context.Context) ([]Resource, error) {
	groups, err := e.drivers.Logging.ListLogGroups(ctx, scope.Scope{})
	if err != nil {
		return nil, fmt.Errorf("walkLogging: %w", err)
	}

	return e.emitSimple(ServiceLogging, TypeLogGroup, len(groups),
		func(i int) (string, string, map[string]string) {
			return groups[i].Name, groups[i].ResourceID, groups[i].Tags
		}), nil
}

// walkCache surfaces in-memory cache clusters (ElastiCache / Memorystore /
// Azure Cache for Redis) so they appear in the inventory/search APIs.
func (e *Engine) walkCache(ctx context.Context) ([]Resource, error) {
	caches, err := e.drivers.Cache.ListCaches(ctx, scope.Scope{})
	if err != nil {
		return nil, fmt.Errorf("walkCache: %w", err)
	}

	return e.emitSimple(ServiceCache, TypeCacheCluster, len(caches),
		func(i int) (string, string, map[string]string) {
			return caches[i].Name, caches[i].Endpoint, caches[i].Tags
		}), nil
}

// walkLoadBalancer surfaces load balancers (ELB/ALB/NLB / Azure Load Balancer /
// GCP forwarding rules) so they appear in the inventory/search APIs.
func (e *Engine) walkLoadBalancer(ctx context.Context) ([]Resource, error) {
	lbs, err := e.drivers.LoadBalancer.DescribeLoadBalancers(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("walkLoadBalancer: %w", err)
	}

	return e.emitSimple(ServiceLB, TypeLoadBalancer, len(lbs),
		func(i int) (string, string, map[string]string) {
			return lbs[i].Name, lbs[i].ARN, lbs[i].Tags
		}), nil
}

// walkMonitoring surfaces metric alarms (CloudWatch alarms / Azure metric
// alerts / GCP alert policies) so they appear in the inventory/search APIs.
func (e *Engine) walkMonitoring(ctx context.Context) ([]Resource, error) {
	alarms, err := e.drivers.Monitoring.DescribeAlarms(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("walkMonitoring: %w", err)
	}

	return e.emitSimple(ServiceMonitoring, TypeAlarm, len(alarms),
		func(i int) (string, string, map[string]string) {
			return alarms[i].Name, "", nil
		}), nil
}

// walkIAM surfaces identity resources — users, roles, policies, and groups
// (IAM / Azure managed identities & role definitions / GCP service accounts &
// roles) — so they appear in the inventory/search APIs.
func (e *Engine) walkIAM(ctx context.Context) ([]Resource, error) {
	users, err := e.drivers.IAM.ListUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("walkIAM users: %w", err)
	}

	roles, err := e.drivers.IAM.ListRoles(ctx)
	if err != nil {
		return nil, fmt.Errorf("walkIAM roles: %w", err)
	}

	policies, err := e.drivers.IAM.ListPolicies(ctx)
	if err != nil {
		return nil, fmt.Errorf("walkIAM policies: %w", err)
	}

	groups, err := e.drivers.IAM.ListGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("walkIAM groups: %w", err)
	}

	out := e.emitSimple(ServiceIAM, TypeUser, len(users),
		func(i int) (string, string, map[string]string) {
			return users[i].Name, users[i].ARN, users[i].Tags
		})
	out = append(out, e.emitSimple(ServiceIAM, TypeRole, len(roles),
		func(i int) (string, string, map[string]string) {
			return roles[i].Name, roles[i].ARN, roles[i].Tags
		})...)
	out = append(out, e.emitSimple(ServiceIAM, TypePolicy, len(policies),
		func(i int) (string, string, map[string]string) {
			return policies[i].Name, policies[i].ARN, nil
		})...)
	out = append(out, e.emitSimple(ServiceIAM, TypeGroup, len(groups),
		func(i int) (string, string, map[string]string) {
			return groups[i].Name, groups[i].ARN, nil
		})...)

	return out, nil
}

// walkGeneric surfaces resources from a provider-projected GenericResources
// capability (e.g. ML/GenAI services that have no shared driver interface).
func (e *Engine) walkGeneric(ctx context.Context, gr GenericResources) ([]Resource, error) {
	items, err := gr.DiscoverResources(ctx)
	if err != nil {
		return nil, fmt.Errorf("walkGeneric: %w", err)
	}

	out := make([]Resource, 0, len(items))

	for i := range items {
		d := items[i]

		region := d.Region
		if region == "" {
			region = e.region
		}

		r := Resource{
			Provider: e.provider, Service: d.Service, Type: d.Type,
			ID:     d.ID,
			ARN:    d.ARN,
			Region: region, Tags: copyTags(d.Tags),
		}
		applyAttrs(&r, &d.Attrs)
		out = append(out, r)
	}

	return out, nil
}
