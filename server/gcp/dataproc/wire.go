package dataproc

import (
	dp "google.golang.org/api/dataproc/v1"

	dpdriver "github.com/stackshy/cloudemu/v2/services/dataproc/driver"
)

// toWireCluster maps a driver cluster to the wire shape
// google.golang.org/api/dataproc/v1.Cluster expects.
func toWireCluster(c *dpdriver.Cluster) *dp.Cluster {
	out := &dp.Cluster{
		ProjectId:   c.ProjectID,
		ClusterName: c.ClusterName,
		ClusterUuid: c.ClusterUUID,
		Labels:      c.Labels,
		Config:      toWireConfig(&c.Config),
		Status:      toWireStatus(&c.Status),
	}

	for i := range c.StatusHistory {
		out.StatusHistory = append(out.StatusHistory, toWireStatus(&c.StatusHistory[i]))
	}

	return out
}

func toWireStatus(s *dpdriver.ClusterStatus) *dp.ClusterStatus {
	return &dp.ClusterStatus{
		State:          s.State,
		Detail:         s.Detail,
		StateStartTime: formatTime(s.StateStartTime),
		Substate:       s.Substate,
	}
}

func toWireConfig(cfg *dpdriver.ClusterConfig) *dp.ClusterConfig {
	out := &dp.ClusterConfig{
		ConfigBucket:          cfg.ConfigBucket,
		TempBucket:            cfg.TempBucket,
		GceClusterConfig:      toWireGce(cfg.GceClusterConfig),
		MasterConfig:          toWireGroup(cfg.MasterConfig),
		WorkerConfig:          toWireGroup(cfg.WorkerConfig),
		SecondaryWorkerConfig: toWireGroup(cfg.SecondaryWorkerConfig),
		SoftwareConfig:        toWireSoftware(cfg.SoftwareConfig),
	}

	for i := range cfg.InitializationActions {
		a := cfg.InitializationActions[i]
		out.InitializationActions = append(out.InitializationActions, &dp.NodeInitializationAction{
			ExecutableFile:   a.ExecutableFile,
			ExecutionTimeout: a.ExecutionTimeout,
		})
	}

	return out
}

func toWireGce(g *dpdriver.GceClusterConfig) *dp.GceClusterConfig {
	if g == nil {
		return nil
	}

	return &dp.GceClusterConfig{
		ZoneUri:        g.ZoneURI,
		NetworkUri:     g.NetworkURI,
		SubnetworkUri:  g.SubnetworkURI,
		ServiceAccount: g.ServiceAccount,
		InternalIpOnly: g.InternalIPOnly,
		Metadata:       g.Metadata,
		Tags:           g.Tags,
	}
}

func toWireGroup(g *dpdriver.InstanceGroupConfig) *dp.InstanceGroupConfig {
	if g == nil {
		return nil
	}

	out := &dp.InstanceGroupConfig{
		NumInstances:   g.NumInstances,
		MachineTypeUri: g.MachineTypeURI,
		ImageUri:       g.ImageURI,
		MinCpuPlatform: g.MinCPUPlatform,
		IsPreemptible:  g.IsPreemptible,
		Preemptibility: g.Preemptibility,
		InstanceNames:  g.InstanceNames,
	}

	if g.DiskConfig != nil {
		out.DiskConfig = &dp.DiskConfig{
			BootDiskType:   g.DiskConfig.BootDiskType,
			BootDiskSizeGb: g.DiskConfig.BootDiskSizeGb,
			NumLocalSsds:   g.DiskConfig.NumLocalSsds,
		}
	}

	return out
}

func toWireSoftware(s *dpdriver.SoftwareConfig) *dp.SoftwareConfig {
	if s == nil {
		return nil
	}

	return &dp.SoftwareConfig{
		ImageVersion:       s.ImageVersion,
		Properties:         s.Properties,
		OptionalComponents: s.OptionalComponents,
	}
}

// fromWireConfig maps a decoded wire ClusterConfig (may be nil) to the driver
// config, echoing every field the caller supplied so a read round-trips.
func fromWireConfig(cfg *dp.ClusterConfig) dpdriver.ClusterConfig {
	if cfg == nil {
		return dpdriver.ClusterConfig{}
	}

	out := dpdriver.ClusterConfig{
		ConfigBucket:          cfg.ConfigBucket,
		TempBucket:            cfg.TempBucket,
		GceClusterConfig:      fromWireGce(cfg.GceClusterConfig),
		MasterConfig:          fromWireGroup(cfg.MasterConfig),
		WorkerConfig:          fromWireGroup(cfg.WorkerConfig),
		SecondaryWorkerConfig: fromWireGroup(cfg.SecondaryWorkerConfig),
		SoftwareConfig:        fromWireSoftware(cfg.SoftwareConfig),
	}

	for _, a := range cfg.InitializationActions {
		out.InitializationActions = append(out.InitializationActions, dpdriver.NodeInitializationAction{
			ExecutableFile:   a.ExecutableFile,
			ExecutionTimeout: a.ExecutionTimeout,
		})
	}

	return out
}

func fromWireGce(g *dp.GceClusterConfig) *dpdriver.GceClusterConfig {
	if g == nil {
		return nil
	}

	return &dpdriver.GceClusterConfig{
		ZoneURI:        g.ZoneUri,
		NetworkURI:     g.NetworkUri,
		SubnetworkURI:  g.SubnetworkUri,
		ServiceAccount: g.ServiceAccount,
		InternalIPOnly: g.InternalIpOnly,
		Metadata:       g.Metadata,
		Tags:           g.Tags,
	}
}

func fromWireGroup(g *dp.InstanceGroupConfig) *dpdriver.InstanceGroupConfig {
	if g == nil {
		return nil
	}

	out := &dpdriver.InstanceGroupConfig{
		NumInstances:   g.NumInstances,
		MachineTypeURI: g.MachineTypeUri,
		ImageURI:       g.ImageUri,
		MinCPUPlatform: g.MinCpuPlatform,
		IsPreemptible:  g.IsPreemptible,
		Preemptibility: g.Preemptibility,
	}

	if g.DiskConfig != nil {
		out.DiskConfig = &dpdriver.DiskConfig{
			BootDiskType:   g.DiskConfig.BootDiskType,
			BootDiskSizeGb: g.DiskConfig.BootDiskSizeGb,
			NumLocalSsds:   g.DiskConfig.NumLocalSsds,
		}
	}

	return out
}

func fromWireSoftware(s *dp.SoftwareConfig) *dpdriver.SoftwareConfig {
	if s == nil {
		return nil
	}

	return &dpdriver.SoftwareConfig{
		ImageVersion:       s.ImageVersion,
		Properties:         s.Properties,
		OptionalComponents: s.OptionalComponents,
	}
}
