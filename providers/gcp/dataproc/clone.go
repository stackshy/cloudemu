package dataproc

import (
	dpdriver "github.com/stackshy/cloudemu/v2/services/dataproc/driver"
)

// cloneCluster returns a deep copy of c so a stored cluster is never aliased by a
// value handed back to a caller (which the wire layer would otherwise be free to
// mutate). Every nested pointer, slice, and map is copied.
func cloneCluster(c *dpdriver.Cluster) dpdriver.Cluster {
	out := *c
	out.Labels = copyLabels(c.Labels)
	out.Config = cloneConfig(&c.Config)
	out.StatusHistory = append([]dpdriver.ClusterStatus(nil), c.StatusHistory...)

	return out
}

func cloneConfig(cfg *dpdriver.ClusterConfig) dpdriver.ClusterConfig {
	out := *cfg
	out.GceClusterConfig = cloneGce(cfg.GceClusterConfig)
	out.MasterConfig = cloneGroup(cfg.MasterConfig)
	out.WorkerConfig = cloneGroup(cfg.WorkerConfig)
	out.SecondaryWorkerConfig = cloneGroup(cfg.SecondaryWorkerConfig)
	out.SoftwareConfig = cloneSoftware(cfg.SoftwareConfig)
	out.InitializationActions = append([]dpdriver.NodeInitializationAction(nil), cfg.InitializationActions...)

	return out
}

func cloneGce(g *dpdriver.GceClusterConfig) *dpdriver.GceClusterConfig {
	if g == nil {
		return nil
	}

	out := *g
	out.Metadata = copyLabels(g.Metadata)
	out.Tags = append([]string(nil), g.Tags...)

	return &out
}

func cloneGroup(g *dpdriver.InstanceGroupConfig) *dpdriver.InstanceGroupConfig {
	if g == nil {
		return nil
	}

	out := *g
	out.InstanceNames = append([]string(nil), g.InstanceNames...)

	if g.DiskConfig != nil {
		dc := *g.DiskConfig
		out.DiskConfig = &dc
	}

	return &out
}

func cloneSoftware(s *dpdriver.SoftwareConfig) *dpdriver.SoftwareConfig {
	if s == nil {
		return nil
	}

	out := *s
	out.Properties = copyLabels(s.Properties)
	out.OptionalComponents = append([]string(nil), s.OptionalComponents...)

	return &out
}
