package bigtable

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	bt "google.golang.org/api/bigtableadmin/v2"

	btdriver "github.com/stackshy/cloudemu/v2/services/bigtable/driver"
)

func toWireInstance(i *btdriver.Instance) *bt.Instance {
	return &bt.Instance{
		Name: i.Name, DisplayName: i.DisplayName, Type: i.Type, State: i.State, Labels: i.Labels,
	}
}

func toWireCluster(c *btdriver.Cluster) *bt.Cluster {
	out := &bt.Cluster{
		Name: c.Name, Location: c.Location, ServeNodes: int64(c.ServeNodes),
		DefaultStorageType: c.DefaultStorageType, State: c.State,
	}

	if a := c.Autoscaling; a != nil {
		out.ClusterConfig = &bt.ClusterConfig{ClusterAutoscalingConfig: &bt.ClusterAutoscalingConfig{
			AutoscalingLimits: &bt.AutoscalingLimits{
				MinServeNodes: int64(a.MinServeNodes), MaxServeNodes: int64(a.MaxServeNodes),
			},
			AutoscalingTargets: &bt.AutoscalingTargets{
				CpuUtilizationPercent: int64(a.CPUTargetPct), StorageUtilizationGibPerNode: int64(a.StorageTargetB),
			},
		}}
	}

	return out
}

func fromWireAutoscaling(c *bt.Cluster) *btdriver.Autoscaling {
	if c.ClusterConfig == nil || c.ClusterConfig.ClusterAutoscalingConfig == nil {
		return nil
	}

	cfg := c.ClusterConfig.ClusterAutoscalingConfig
	a := &btdriver.Autoscaling{}

	if cfg.AutoscalingLimits != nil {
		a.MinServeNodes = int(cfg.AutoscalingLimits.MinServeNodes)
		a.MaxServeNodes = int(cfg.AutoscalingLimits.MaxServeNodes)
	}

	if cfg.AutoscalingTargets != nil {
		a.CPUTargetPct = int(cfg.AutoscalingTargets.CpuUtilizationPercent)
		a.StorageTargetB = int(cfg.AutoscalingTargets.StorageUtilizationGibPerNode)
	}

	return a
}

func toWireGCRule(r *btdriver.GCRule) *bt.GcRule {
	if r == nil {
		return nil
	}

	out := &bt.GcRule{MaxNumVersions: int64(r.MaxNumVersions)}
	if r.MaxAgeSeconds > 0 {
		out.MaxAge = strconv.FormatInt(r.MaxAgeSeconds, 10) + "s"
	}

	for i := range r.Union {
		if out.Union == nil {
			out.Union = &bt.Union{}
		}

		out.Union.Rules = append(out.Union.Rules, toWireGCRule(&r.Union[i]))
	}

	for i := range r.Intersection {
		if out.Intersection == nil {
			out.Intersection = &bt.Intersection{}
		}

		out.Intersection.Rules = append(out.Intersection.Rules, toWireGCRule(&r.Intersection[i]))
	}

	return out
}

func fromWireGCRule(g *bt.GcRule) *btdriver.GCRule {
	if g == nil {
		return nil
	}

	r := &btdriver.GCRule{MaxNumVersions: int(g.MaxNumVersions)}
	if g.MaxAge != "" {
		r.MaxAgeSeconds = parseDurationSeconds(g.MaxAge)
	}

	if g.Union != nil {
		for _, x := range g.Union.Rules {
			r.Union = append(r.Union, *fromWireGCRule(x))
		}
	}

	if g.Intersection != nil {
		for _, x := range g.Intersection.Rules {
			r.Intersection = append(r.Intersection, *fromWireGCRule(x))
		}
	}

	return r
}

func parseDurationSeconds(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSuffix(s, "s"), 10, 64)

	return n
}

func toWireTable(t *btdriver.Table) *bt.Table {
	out := &bt.Table{
		Name: t.Name, Granularity: t.Granularity, DeletionProtection: t.DeletionProtection,
	}

	if len(t.ColumnFamilies) > 0 {
		out.ColumnFamilies = make(map[string]bt.ColumnFamily, len(t.ColumnFamilies))
		for k, v := range t.ColumnFamilies {
			out.ColumnFamilies[k] = bt.ColumnFamily{GcRule: toWireGCRule(v.GCRule)}
		}
	}

	if t.SourceBackup != "" {
		out.RestoreInfo = &bt.RestoreInfo{SourceType: "BACKUP"}
	}

	return out
}

func fromWireColumnFamilies(src map[string]bt.ColumnFamily) map[string]btdriver.ColumnFamily {
	if len(src) == 0 {
		return nil
	}

	out := make(map[string]btdriver.ColumnFamily, len(src))
	for k, v := range src {
		out[k] = btdriver.ColumnFamily{GCRule: fromWireGCRule(v.GcRule)}
	}

	return out
}

func toWireAppProfile(a *btdriver.AppProfile) *bt.AppProfile {
	out := &bt.AppProfile{
		Name: a.Name, Description: a.Description, Etag: a.Etag, Priority: a.Priority,
	}

	if a.MultiClusterRoutingAny {
		out.MultiClusterRoutingUseAny = &bt.MultiClusterRoutingUseAny{ClusterIds: a.MultiClusterClusterIDs}
	} else if a.SingleClusterID != "" {
		out.SingleClusterRouting = &bt.SingleClusterRouting{
			ClusterId: a.SingleClusterID, AllowTransactionalWrites: a.AllowTransactionalWrites,
		}
	}

	return out
}

func fromWireAppProfile(parent, id string, a *bt.AppProfile) btdriver.CreateAppProfileConfig {
	cfg := btdriver.CreateAppProfileConfig{
		Parent: parent, AppProfileID: id, Description: a.Description, Priority: a.Priority,
	}

	if a.MultiClusterRoutingUseAny != nil {
		cfg.MultiClusterRoutingAny = true
		cfg.MultiClusterClusterIDs = a.MultiClusterRoutingUseAny.ClusterIds
	}

	if a.SingleClusterRouting != nil {
		cfg.SingleClusterID = a.SingleClusterRouting.ClusterId
		cfg.AllowTransactionalWrites = a.SingleClusterRouting.AllowTransactionalWrites
	}

	return cfg
}

func toWireBackup(b *btdriver.Backup) *bt.Backup {
	out := &bt.Backup{
		Name: b.Name, SourceTable: b.SourceTable, SourceBackup: b.SourceBackup,
		SizeBytes: b.SizeBytes, State: b.State, BackupType: b.BackupType,
	}
	if !b.ExpireTime.IsZero() {
		out.ExpireTime = b.ExpireTime.Format(time.RFC3339)
	}

	if !b.StartTime.IsZero() {
		out.StartTime = b.StartTime.Format(time.RFC3339)
	}

	if !b.EndTime.IsZero() {
		out.EndTime = b.EndTime.Format(time.RFC3339)
	}

	return out
}

func toWirePolicy(p *btdriver.Policy) *bt.Policy {
	out := &bt.Policy{Version: int64(p.Version), Etag: p.Etag}
	for i := range p.Bindings {
		out.Bindings = append(out.Bindings, &bt.Binding{Role: p.Bindings[i].Role, Members: p.Bindings[i].Members})
	}

	return out
}

func fromWirePolicy(p *bt.Policy) btdriver.Policy {
	out := btdriver.Policy{Version: int(p.Version), Etag: p.Etag}
	for _, b := range p.Bindings {
		out.Bindings = append(out.Bindings, btdriver.Binding{Role: b.Role, Members: b.Members})
	}

	return out
}

// doneOp builds a completed LRO carrying the resulting resource as its response.
func doneOp(op *btdriver.Operation, response any) *bt.Operation {
	out := &bt.Operation{Name: op.Name, Done: true}
	if response != nil {
		out.Response = mustRawJSON(response)
	}

	return out
}

func mustRawJSON(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}

	return raw
}

// lastSegment returns the final path segment of a resource name.
func lastSegment(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}

	return name
}

// operationName normalises an "operations/..."-suffixed path to the stored key.
func operationName(path string) string {
	if i := strings.Index(path, "operations/"); i >= 0 {
		return path[i:]
	}

	return path
}
