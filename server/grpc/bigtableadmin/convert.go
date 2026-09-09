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

// normalizeMaskPath lowercases a single field-mask path and strips underscores,
// so snake_case and camelCase spellings of the same path compare equal
// ("display_name" and "displayName" both become "displayname").
func normalizeMaskPath(s string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), "_", ""))
}

// normalizeMaskPaths lowercases each mask path and strips underscores, matching
// the token form the bigtable store's UpdateInstanceConfig.UpdateMask expects
// (so "display_name" and "displayName" both become "displayname").
func normalizeMaskPaths(paths []string) []string {
	out := make([]string, 0, len(paths))

	for _, p := range paths {
		if n := normalizeMaskPath(p); n != "" {
			out = append(out, n)
		}
	}

	return out
}

// ---- app profiles ----

func appProfilePriority(s string) adminpb.AppProfile_Priority {
	return adminpb.AppProfile_Priority(adminpb.AppProfile_Priority_value[s])
}

// toProtoAppProfile maps the driver's flat app profile to the proto oneofs. The
// routing policy is exactly one of multi-cluster / single-cluster, and priority
// is echoed through the modern standard_isolation field (what the real API
// returns), so terraform reads back the routing + isolation it set.
func toProtoAppProfile(a *btdriver.AppProfile) *adminpb.AppProfile {
	out := &adminpb.AppProfile{Name: a.Name, Description: a.Description, Etag: a.Etag}

	switch {
	case a.MultiClusterRoutingAny:
		out.RoutingPolicy = &adminpb.AppProfile_MultiClusterRoutingUseAny_{
			MultiClusterRoutingUseAny: &adminpb.AppProfile_MultiClusterRoutingUseAny{ClusterIds: a.MultiClusterClusterIDs},
		}
	case a.SingleClusterID != "":
		out.RoutingPolicy = &adminpb.AppProfile_SingleClusterRouting_{
			SingleClusterRouting: &adminpb.AppProfile_SingleClusterRouting{
				ClusterId: a.SingleClusterID, AllowTransactionalWrites: a.AllowTransactionalWrites,
			},
		}
	}

	if p := appProfilePriority(a.Priority); p != adminpb.AppProfile_PRIORITY_UNSPECIFIED {
		out.Isolation = &adminpb.AppProfile_StandardIsolation_{
			StandardIsolation: &adminpb.AppProfile_StandardIsolation{Priority: p},
		}
	}

	return out
}

func fromProtoAppProfile(parent, id string, a *adminpb.AppProfile) btdriver.CreateAppProfileConfig {
	cfg := btdriver.CreateAppProfileConfig{Parent: parent, AppProfileID: id, Description: a.GetDescription()}

	if m := a.GetMultiClusterRoutingUseAny(); m != nil {
		cfg.MultiClusterRoutingAny = true
		cfg.MultiClusterClusterIDs = m.GetClusterIds()
	}

	if sc := a.GetSingleClusterRouting(); sc != nil {
		cfg.SingleClusterID = sc.GetClusterId()
		cfg.AllowTransactionalWrites = sc.GetAllowTransactionalWrites()
	}

	// Priority is read from the modern standard_isolation field (what the real
	// API and terraform send); the deprecated top-level priority oneof is not
	// consulted.
	if si := a.GetStandardIsolation(); si != nil {
		cfg.Priority = si.GetPriority().String()
	}

	return cfg
}

// maskSet is a normalized google.protobuf.FieldMask used to overlay only
// the named fields of an app-profile update onto the current profile. A nil mask
// means the caller sent none, so the update replaces every field from the body.
type maskSet struct{ paths []string }

func newMaskSet(paths []string) *maskSet {
	norm := normalizeMaskPaths(paths)
	if len(norm) == 0 {
		return nil
	}

	return &maskSet{paths: norm}
}

// has reports whether the mask names field exactly or as the leading segment of
// a dotted sub-path. field is normalized by this method.
func (m *maskSet) has(field string) bool {
	field = normalizeMaskPath(field)
	for _, p := range m.paths {
		if p == field || strings.HasPrefix(p, field+".") {
			return true
		}
	}

	return false
}

// contains reports whether any masked path contains sub as a substring, used for
// the routing/isolation paths whose exact spelling varies across clients. sub is
// normalized by this method.
func (m *maskSet) contains(sub string) bool {
	sub = normalizeMaskPath(sub)
	for _, p := range m.paths {
		if strings.Contains(p, sub) {
			return true
		}
	}

	return false
}

// mergeAppProfileConfig overlays the fields named by mask (from body) onto the
// current profile, keeping every unmasked field at its current value. Routing is
// treated as one unit: any routing path in the mask replaces the whole policy.
func mergeAppProfileConfig(
	parent, id string, cur *btdriver.AppProfile, body *adminpb.AppProfile, mask *maskSet,
) btdriver.CreateAppProfileConfig {
	cfg := btdriver.CreateAppProfileConfig{
		Parent:                   parent,
		AppProfileID:             id,
		Description:              cur.Description,
		MultiClusterRoutingAny:   cur.MultiClusterRoutingAny,
		MultiClusterClusterIDs:   cur.MultiClusterClusterIDs,
		SingleClusterID:          cur.SingleClusterID,
		AllowTransactionalWrites: cur.AllowTransactionalWrites,
		Priority:                 cur.Priority,
	}

	if mask.has("description") {
		cfg.Description = body.GetDescription()
	}

	if mask.contains("routing") {
		b := fromProtoAppProfile(parent, id, body)
		cfg.MultiClusterRoutingAny = b.MultiClusterRoutingAny
		cfg.MultiClusterClusterIDs = b.MultiClusterClusterIDs
		cfg.SingleClusterID = b.SingleClusterID
		cfg.AllowTransactionalWrites = b.AllowTransactionalWrites
	}

	if mask.contains("priority") || mask.contains("isolation") {
		cfg.Priority = fromProtoAppProfile(parent, id, body).Priority
	}

	return cfg
}
