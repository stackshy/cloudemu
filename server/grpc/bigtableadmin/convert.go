package bigtableadmin

import (
	"strings"
	"time"

	adminpb "cloud.google.com/go/bigtable/admin/apiv2/adminpb"
	iampb "cloud.google.com/go/iam/apiv1/iampb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	btdriver "github.com/stackshy/cloudemu/v2/services/bigtable/driver"
)

// i32 narrows a small, non-negative bigtable config value (serve nodes,
// autoscaling limits/targets, version, GC max-versions) to the int32 the proto
// types use.
//
//nolint:gosec // G115: these are small, non-negative node/limit/count inputs
func i32(n int) int32 {
	return int32(n)
}

// ---- enum <-> driver-string helpers ----

func instanceType(s string) adminpb.Instance_Type {
	return adminpb.Instance_Type(adminpb.Instance_Type_value[s])
}

// typeName returns the driver's string spelling for an instance type, or "" for
// the unspecified value so the store applies its own default (PRODUCTION).
func typeName(t adminpb.Instance_Type) string {
	if t == adminpb.Instance_TYPE_UNSPECIFIED {
		return ""
	}

	return t.String()
}

func instanceState(s string) adminpb.Instance_State {
	return adminpb.Instance_State(adminpb.Instance_State_value[s])
}

func storageType(s string) adminpb.StorageType {
	return adminpb.StorageType(adminpb.StorageType_value[s])
}

// storageTypeName maps a proto storage type to the driver string, or "" for
// unspecified so the store applies its default (SSD).
func storageTypeName(t adminpb.StorageType) string {
	if t == adminpb.StorageType_STORAGE_TYPE_UNSPECIFIED {
		return ""
	}

	return t.String()
}

func clusterState(s string) adminpb.Cluster_State {
	return adminpb.Cluster_State(adminpb.Cluster_State_value[s])
}

func tableGranularity(s string) adminpb.Table_TimestampGranularity {
	return adminpb.Table_TimestampGranularity(adminpb.Table_TimestampGranularity_value[s])
}

func granularityName(g adminpb.Table_TimestampGranularity) string {
	if g == adminpb.Table_TIMESTAMP_GRANULARITY_UNSPECIFIED {
		return ""
	}

	return g.String()
}

// ---- instances ----

func toProtoInstance(i *btdriver.Instance) *adminpb.Instance {
	out := &adminpb.Instance{
		Name:        i.Name,
		DisplayName: i.DisplayName,
		Type:        instanceType(i.Type),
		State:       instanceState(i.State),
		Labels:      i.Labels,
	}
	if !i.CreateTime.IsZero() {
		out.CreateTime = timestamppb.New(i.CreateTime)
	}

	return out
}

// ---- clusters ----

func toProtoCluster(c *btdriver.Cluster) *adminpb.Cluster {
	out := &adminpb.Cluster{
		Name:               c.Name,
		Location:           c.Location,
		ServeNodes:         i32(c.ServeNodes),
		DefaultStorageType: storageType(c.DefaultStorageType),
		State:              clusterState(c.State),
	}

	if a := c.Autoscaling; a != nil {
		out.Config = &adminpb.Cluster_ClusterConfig_{ClusterConfig: &adminpb.Cluster_ClusterConfig{
			ClusterAutoscalingConfig: &adminpb.Cluster_ClusterAutoscalingConfig{
				AutoscalingLimits: &adminpb.AutoscalingLimits{
					MinServeNodes: i32(a.MinServeNodes), MaxServeNodes: i32(a.MaxServeNodes),
				},
				AutoscalingTargets: &adminpb.AutoscalingTargets{
					CpuUtilizationPercent: i32(a.CPUTargetPct), StorageUtilizationGibPerNode: i32(a.StorageTargetB),
				},
			},
		}}
	}

	return out
}

func fromProtoAutoscaling(c *adminpb.Cluster) *btdriver.Autoscaling {
	cfg := c.GetClusterConfig().GetClusterAutoscalingConfig()
	if cfg == nil {
		return nil
	}

	a := &btdriver.Autoscaling{}

	if l := cfg.GetAutoscalingLimits(); l != nil {
		a.MinServeNodes = int(l.GetMinServeNodes())
		a.MaxServeNodes = int(l.GetMaxServeNodes())
	}

	if t := cfg.GetAutoscalingTargets(); t != nil {
		a.CPUTargetPct = int(t.GetCpuUtilizationPercent())
		a.StorageTargetB = int(t.GetStorageUtilizationGibPerNode())
	}

	return a
}

func clusterConfig(name string, c *adminpb.Cluster) btdriver.CreateClusterConfig {
	return btdriver.CreateClusterConfig{
		Name:               name,
		Location:           c.GetLocation(),
		ServeNodes:         int(c.GetServeNodes()),
		DefaultStorageType: storageTypeName(c.GetDefaultStorageType()),
		Autoscaling:        fromProtoAutoscaling(c),
	}
}

// ---- tables ----

func toProtoTable(t *btdriver.Table) *adminpb.Table {
	out := &adminpb.Table{
		Name:               t.Name,
		Granularity:        tableGranularity(t.Granularity),
		DeletionProtection: t.DeletionProtection,
	}

	if len(t.ColumnFamilies) > 0 {
		out.ColumnFamilies = make(map[string]*adminpb.ColumnFamily, len(t.ColumnFamilies))
		for k, v := range t.ColumnFamilies {
			out.ColumnFamilies[k] = &adminpb.ColumnFamily{GcRule: toProtoGCRule(v.GCRule)}
		}
	}

	if t.SourceBackup != "" {
		out.RestoreInfo = &adminpb.RestoreInfo{SourceType: adminpb.RestoreSourceType_BACKUP}
	}

	return out
}

func fromProtoColumnFamilies(src map[string]*adminpb.ColumnFamily) map[string]btdriver.ColumnFamily {
	if len(src) == 0 {
		return nil
	}

	out := make(map[string]btdriver.ColumnFamily, len(src))
	for k, v := range src {
		out[k] = btdriver.ColumnFamily{GCRule: fromProtoGCRule(v.GetGcRule())}
	}

	return out
}

// toProtoGCRule maps the driver's flat rule to the proto oneof. A column-family
// GC rule is exactly one of union / intersection / max-age / max-versions, so a
// fixed precedence picks the single variant the oneof allows.
func toProtoGCRule(r *btdriver.GCRule) *adminpb.GcRule {
	if r == nil {
		return nil
	}

	switch {
	case len(r.Union) > 0:
		u := &adminpb.GcRule_Union{}
		for i := range r.Union {
			u.Rules = append(u.Rules, toProtoGCRule(&r.Union[i]))
		}

		return &adminpb.GcRule{Rule: &adminpb.GcRule_Union_{Union: u}}
	case len(r.Intersection) > 0:
		is := &adminpb.GcRule_Intersection{}
		for i := range r.Intersection {
			is.Rules = append(is.Rules, toProtoGCRule(&r.Intersection[i]))
		}

		return &adminpb.GcRule{Rule: &adminpb.GcRule_Intersection_{Intersection: is}}
	case r.MaxAgeSeconds > 0:
		return &adminpb.GcRule{Rule: &adminpb.GcRule_MaxAge{MaxAge: durationpb.New(time.Duration(r.MaxAgeSeconds) * time.Second)}}
	case r.MaxNumVersions > 0:
		return &adminpb.GcRule{Rule: &adminpb.GcRule_MaxNumVersions{MaxNumVersions: i32(r.MaxNumVersions)}}
	default:
		return nil
	}
}

func fromProtoGCRule(g *adminpb.GcRule) *btdriver.GCRule {
	if g == nil {
		return nil
	}

	out := &btdriver.GCRule{}

	switch r := g.GetRule().(type) {
	case *adminpb.GcRule_MaxNumVersions:
		out.MaxNumVersions = int(r.MaxNumVersions)
	case *adminpb.GcRule_MaxAge:
		out.MaxAgeSeconds = int64(r.MaxAge.AsDuration() / time.Second)
	case *adminpb.GcRule_Intersection_:
		for _, x := range r.Intersection.GetRules() {
			out.Intersection = append(out.Intersection, *fromProtoGCRule(x))
		}
	case *adminpb.GcRule_Union_:
		for _, x := range r.Union.GetRules() {
			out.Union = append(out.Union, *fromProtoGCRule(x))
		}
	}

	return out
}

// ---- IAM ----

func toProtoPolicy(p *btdriver.Policy) *iampb.Policy {
	out := &iampb.Policy{Version: i32(p.Version), Etag: []byte(p.Etag)}
	for i := range p.Bindings {
		out.Bindings = append(out.Bindings, &iampb.Binding{Role: p.Bindings[i].Role, Members: p.Bindings[i].Members})
	}

	return out
}

func fromProtoPolicy(p *iampb.Policy) btdriver.Policy {
	out := btdriver.Policy{Version: int(p.GetVersion()), Etag: string(p.GetEtag())}
	for _, b := range p.GetBindings() {
		out.Bindings = append(out.Bindings, btdriver.Binding{Role: b.GetRole(), Members: b.GetMembers()})
	}

	return out
}

// ---- misc ----

func lastSegment(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}

	return name
}

// normalizeMaskPaths lowercases each mask path and strips underscores, matching
// the token form the bigtable store's UpdateInstanceConfig.UpdateMask expects
// (so "display_name" and "displayName" both become "displayname").
func normalizeMaskPaths(paths []string) []string {
	out := make([]string, 0, len(paths))

	for _, p := range paths {
		if n := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(p), "_", "")); n != "" {
			out = append(out, n)
		}
	}

	return out
}
