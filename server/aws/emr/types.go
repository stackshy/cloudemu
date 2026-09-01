package emr

import "time"

// --- Request inputs (the members the handler reads) ---

// runJobFlowInput mirrors the members of the SDK RunJobFlowInput this handler
// reads.
type runJobFlowInput struct {
	Name              *string                      `json:"Name"`
	ReleaseLabel      *string                      `json:"ReleaseLabel"`
	LogURI            *string                      `json:"LogUri"`
	ServiceRole       *string                      `json:"ServiceRole"`
	JobFlowRole       *string                      `json:"JobFlowRole"`
	VisibleToAllUsers *bool                        `json:"VisibleToAllUsers"`
	Instances         *jobFlowInstancesConfig      `json:"Instances"`
	Applications      []applicationInput           `json:"Applications"`
	Tags              []tagInput                   `json:"Tags"`
	Steps             []stepConfig                 `json:"Steps"`
	BootstrapActions  []bootstrapActionConfigInput `json:"BootstrapActions"`
}

// jobFlowInstancesConfig mirrors the read members of the SDK
// JobFlowInstancesConfig.
type jobFlowInstancesConfig struct {
	Ec2SubnetID                 *string                    `json:"Ec2SubnetId"`
	Ec2KeyName                  *string                    `json:"Ec2KeyName"`
	MasterInstanceType          *string                    `json:"MasterInstanceType"`
	SlaveInstanceType           *string                    `json:"SlaveInstanceType"`
	InstanceCount               *int32                     `json:"InstanceCount"`
	KeepJobFlowAliveWhenNoSteps *bool                      `json:"KeepJobFlowAliveWhenNoSteps"`
	TerminationProtected        *bool                      `json:"TerminationProtected"`
	InstanceGroups              []instanceGroupConfigInput `json:"InstanceGroups"`
}

// instanceGroupConfigInput mirrors the read members of the SDK
// InstanceGroupConfig.
type instanceGroupConfigInput struct {
	Name          *string `json:"Name"`
	InstanceRole  string  `json:"InstanceRole"`
	InstanceType  *string `json:"InstanceType"`
	InstanceCount *int32  `json:"InstanceCount"`
	Market        string  `json:"Market"`
	BidPrice      *string `json:"BidPrice"`
}

// bootstrapActionConfigInput mirrors the SDK BootstrapActionConfig.
type bootstrapActionConfigInput struct {
	Name                  *string                           `json:"Name"`
	ScriptBootstrapAction *scriptBootstrapActionConfigInput `json:"ScriptBootstrapAction"`
}

// scriptBootstrapActionConfigInput mirrors the SDK ScriptBootstrapActionConfig.
type scriptBootstrapActionConfigInput struct {
	Path *string  `json:"Path"`
	Args []string `json:"Args"`
}

// applicationInput mirrors the SDK Application on the request path.
type applicationInput struct {
	Name    *string  `json:"Name"`
	Version *string  `json:"Version"`
	Args    []string `json:"Args"`
}

// tagInput mirrors the SDK Tag on the request path.
type tagInput struct {
	Key   *string `json:"Key"`
	Value *string `json:"Value"`
}

// stepConfig mirrors the SDK StepConfig.
type stepConfig struct {
	Name            *string              `json:"Name"`
	ActionOnFailure string               `json:"ActionOnFailure"`
	HadoopJarStep   *hadoopJarStepConfig `json:"HadoopJarStep"`
}

// hadoopJarStepConfig mirrors the SDK HadoopJarStepConfig.
type hadoopJarStepConfig struct {
	Jar        *string        `json:"Jar"`
	MainClass  *string        `json:"MainClass"`
	Args       []string       `json:"Args"`
	Properties []keyValueWire `json:"Properties"`
}

// keyValueWire mirrors the SDK KeyValue.
type keyValueWire struct {
	Key   *string `json:"Key"`
	Value *string `json:"Value"`
}

// describeClusterInput mirrors the SDK DescribeClusterInput.
type describeClusterInput struct {
	ClusterID *string `json:"ClusterId"`
}

// listClustersInput mirrors the read members of the SDK ListClustersInput.
type listClustersInput struct {
	ClusterStates []string `json:"ClusterStates"`
	CreatedAfter  *float64 `json:"CreatedAfter"`
	CreatedBefore *float64 `json:"CreatedBefore"`
}

// terminateJobFlowsInput mirrors the SDK TerminateJobFlowsInput.
type terminateJobFlowsInput struct {
	JobFlowIDs []string `json:"JobFlowIds"`
}

// addJobFlowStepsInput mirrors the SDK AddJobFlowStepsInput.
type addJobFlowStepsInput struct {
	JobFlowID *string      `json:"JobFlowId"`
	Steps     []stepConfig `json:"Steps"`
}

// listStepsInput mirrors the read members of the SDK ListStepsInput.
type listStepsInput struct {
	ClusterID  *string  `json:"ClusterId"`
	StepStates []string `json:"StepStates"`
	StepIDs    []string `json:"StepIds"`
}

// describeStepInput mirrors the SDK DescribeStepInput.
type describeStepInput struct {
	ClusterID *string `json:"ClusterId"`
	StepID    *string `json:"StepId"`
}

// cancelStepsInput mirrors the read members of the SDK CancelStepsInput.
type cancelStepsInput struct {
	ClusterID *string  `json:"ClusterId"`
	StepIDs   []string `json:"StepIds"`
}

// addInstanceGroupsInput mirrors the SDK AddInstanceGroupsInput.
type addInstanceGroupsInput struct {
	JobFlowID      *string                    `json:"JobFlowId"`
	InstanceGroups []instanceGroupConfigInput `json:"InstanceGroups"`
}

// modifyInstanceGroupsInput mirrors the SDK ModifyInstanceGroupsInput.
type modifyInstanceGroupsInput struct {
	InstanceGroups []instanceGroupModifyInput `json:"InstanceGroups"`
}

// instanceGroupModifyInput mirrors the read members of the SDK
// InstanceGroupModifyConfig.
type instanceGroupModifyInput struct {
	InstanceGroupID *string `json:"InstanceGroupId"`
	InstanceCount   *int32  `json:"InstanceCount"`
}

// listInstanceGroupsInput mirrors the SDK ListInstanceGroupsInput.
type listInstanceGroupsInput struct {
	ClusterID *string `json:"ClusterId"`
}

// listInstancesInput mirrors the read members of the SDK ListInstancesInput.
type listInstancesInput struct {
	ClusterID          *string  `json:"ClusterId"`
	InstanceGroupID    *string  `json:"InstanceGroupId"`
	InstanceGroupTypes []string `json:"InstanceGroupTypes"`
	InstanceStates     []string `json:"InstanceStates"`
}

// listBootstrapActionsInput mirrors the SDK ListBootstrapActionsInput.
type listBootstrapActionsInput struct {
	ClusterID *string `json:"ClusterId"`
}

// --- Response outputs (AWS JSON 1.1 shapes the SDK decodes) ---

// runJobFlowOutput mirrors the SDK RunJobFlowOutput.
type runJobFlowOutput struct {
	JobFlowID  string `json:"JobFlowId"`
	ClusterArn string `json:"ClusterArn"`
}

// addJobFlowStepsOutput mirrors the SDK AddJobFlowStepsOutput.
type addJobFlowStepsOutput struct {
	StepIDs []string `json:"StepIds"`
}

// describeClusterOutput mirrors the SDK DescribeClusterOutput.
type describeClusterOutput struct {
	Cluster *clusterWire `json:"Cluster"`
}

// listClustersOutput mirrors the SDK ListClustersOutput.
type listClustersOutput struct {
	Clusters []clusterSummaryWire `json:"Clusters"`
}

// listStepsOutput mirrors the SDK ListStepsOutput.
type listStepsOutput struct {
	Steps []stepSummaryWire `json:"Steps"`
}

// describeStepOutput mirrors the SDK DescribeStepOutput.
type describeStepOutput struct {
	Step *stepWire `json:"Step"`
}

// cancelStepsOutput mirrors the SDK CancelStepsOutput.
type cancelStepsOutput struct {
	CancelStepsInfoList []cancelStepsInfoWire `json:"CancelStepsInfoList"`
}

// cancelStepsInfoWire mirrors the SDK CancelStepsInfo.
type cancelStepsInfoWire struct {
	StepID string `json:"StepId"`
	Status string `json:"Status"`
	Reason string `json:"Reason,omitempty"`
}

// addInstanceGroupsOutput mirrors the SDK AddInstanceGroupsOutput.
type addInstanceGroupsOutput struct {
	ClusterArn       string   `json:"ClusterArn"`
	JobFlowID        string   `json:"JobFlowId"`
	InstanceGroupIDs []string `json:"InstanceGroupIds"`
}

// listInstanceGroupsOutput mirrors the SDK ListInstanceGroupsOutput.
type listInstanceGroupsOutput struct {
	InstanceGroups []instanceGroupWire `json:"InstanceGroups"`
}

// listInstancesOutput mirrors the SDK ListInstancesOutput.
type listInstancesOutput struct {
	Instances []instanceWire `json:"Instances"`
}

// listBootstrapActionsOutput mirrors the SDK ListBootstrapActionsOutput.
type listBootstrapActionsOutput struct {
	BootstrapActions []commandWire `json:"BootstrapActions"`
}

// instanceGroupWire mirrors the members of the SDK InstanceGroup this handler
// populates.
type instanceGroupWire struct {
	ID                     string     `json:"Id"`
	Name                   string     `json:"Name,omitempty"`
	InstanceGroupType      string     `json:"InstanceGroupType"`
	InstanceType           string     `json:"InstanceType,omitempty"`
	Market                 string     `json:"Market,omitempty"`
	BidPrice               string     `json:"BidPrice,omitempty"`
	RequestedInstanceCount int32      `json:"RequestedInstanceCount"`
	RunningInstanceCount   int32      `json:"RunningInstanceCount"`
	Status                 statusWire `json:"Status"`
}

// instanceWire mirrors the members of the SDK Instance this handler populates.
type instanceWire struct {
	ID              string     `json:"Id"`
	Ec2InstanceID   string     `json:"Ec2InstanceId"`
	InstanceGroupID string     `json:"InstanceGroupId"`
	InstanceType    string     `json:"InstanceType,omitempty"`
	Market          string     `json:"Market,omitempty"`
	PrivateDNSName  string     `json:"PrivateDnsName,omitempty"`
	PrivateIPAddr   string     `json:"PrivateIpAddress,omitempty"`
	Status          statusWire `json:"Status"`
}

// commandWire mirrors the SDK Command (ListBootstrapActions entries).
type commandWire struct {
	Name       string   `json:"Name,omitempty"`
	ScriptPath string   `json:"ScriptPath,omitempty"`
	Args       []string `json:"Args,omitempty"`
}

// stateChangeReasonWire mirrors the SDK ClusterStateChangeReason /
// StepStateChangeReason.
type stateChangeReasonWire struct {
	Code    string `json:"Code,omitempty"`
	Message string `json:"Message,omitempty"`
}

// timelineWire mirrors the SDK ClusterTimeline. Times are epoch seconds, the
// AWS JSON default timestamp encoding.
type timelineWire struct {
	CreationDateTime *float64 `json:"CreationDateTime,omitempty"`
	ReadyDateTime    *float64 `json:"ReadyDateTime,omitempty"`
	EndDateTime      *float64 `json:"EndDateTime,omitempty"`
}

// statusWire mirrors the SDK ClusterStatus.
type statusWire struct {
	State             string                 `json:"State"`
	StateChangeReason *stateChangeReasonWire `json:"StateChangeReason,omitempty"`
	Timeline          *timelineWire          `json:"Timeline,omitempty"`
}

// ec2AttributesWire mirrors the read members of the SDK Ec2InstanceAttributes.
type ec2AttributesWire struct {
	Ec2SubnetID string `json:"Ec2SubnetId,omitempty"`
	Ec2KeyName  string `json:"Ec2KeyName,omitempty"`
}

// applicationWire mirrors the SDK Application on the response path.
type applicationWire struct {
	Name    string   `json:"Name,omitempty"`
	Version string   `json:"Version,omitempty"`
	Args    []string `json:"Args,omitempty"`
}

// tagWire mirrors the SDK Tag on the response path.
type tagWire struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

// clusterWire mirrors the members of the SDK Cluster this handler populates.
type clusterWire struct {
	ID                      string             `json:"Id"`
	Name                    string             `json:"Name"`
	ClusterArn              string             `json:"ClusterArn"`
	ReleaseLabel            string             `json:"ReleaseLabel,omitempty"`
	LogURI                  string             `json:"LogUri,omitempty"`
	ServiceRole             string             `json:"ServiceRole,omitempty"`
	Status                  statusWire         `json:"Status"`
	Ec2InstanceAttributes   *ec2AttributesWire `json:"Ec2InstanceAttributes,omitempty"`
	InstanceCollectionType  string             `json:"InstanceCollectionType,omitempty"`
	AutoTerminate           bool               `json:"AutoTerminate"`
	TerminationProtected    bool               `json:"TerminationProtected"`
	VisibleToAllUsers       bool               `json:"VisibleToAllUsers"`
	NormalizedInstanceHours int32              `json:"NormalizedInstanceHours"`
	Applications            []applicationWire  `json:"Applications,omitempty"`
	Tags                    []tagWire          `json:"Tags,omitempty"`
}

// clusterSummaryWire mirrors the SDK ClusterSummary.
type clusterSummaryWire struct {
	ID                      string     `json:"Id"`
	Name                    string     `json:"Name"`
	ClusterArn              string     `json:"ClusterArn"`
	Status                  statusWire `json:"Status"`
	NormalizedInstanceHours int32      `json:"NormalizedInstanceHours"`
}

// hadoopStepConfigWire mirrors the SDK HadoopStepConfig (the response step config).
type hadoopStepConfigWire struct {
	Jar        string            `json:"Jar,omitempty"`
	MainClass  string            `json:"MainClass,omitempty"`
	Args       []string          `json:"Args,omitempty"`
	Properties map[string]string `json:"Properties,omitempty"`
}

// stepStatusWire mirrors the SDK StepStatus.
type stepStatusWire struct {
	State             string                 `json:"State"`
	StateChangeReason *stateChangeReasonWire `json:"StateChangeReason,omitempty"`
	Timeline          *stepTimelineWire      `json:"Timeline,omitempty"`
}

// stepTimelineWire mirrors the SDK StepTimeline.
type stepTimelineWire struct {
	CreationDateTime *float64 `json:"CreationDateTime,omitempty"`
	StartDateTime    *float64 `json:"StartDateTime,omitempty"`
	EndDateTime      *float64 `json:"EndDateTime,omitempty"`
}

// stepWire mirrors the members of the SDK Step this handler populates.
type stepWire struct {
	ID              string                `json:"Id"`
	Name            string                `json:"Name"`
	Config          *hadoopStepConfigWire `json:"Config,omitempty"`
	ActionOnFailure string                `json:"ActionOnFailure,omitempty"`
	Status          stepStatusWire        `json:"Status"`
}

// stepSummaryWire mirrors the SDK StepSummary.
type stepSummaryWire struct {
	ID              string                `json:"Id"`
	Name            string                `json:"Name"`
	Config          *hadoopStepConfigWire `json:"Config,omitempty"`
	ActionOnFailure string                `json:"ActionOnFailure,omitempty"`
	Status          stepStatusWire        `json:"Status"`
}

// --- Filters ---

// clusterFilter carries the ListClusters state/creation-window predicate.
type clusterFilter struct {
	states        map[string]bool
	createdAfter  *time.Time
	createdBefore *time.Time
}

// matches reports whether cluster c passes the filter.
func (f clusterFilter) matches(c *cluster) bool {
	if len(f.states) > 0 && !f.states[c.state] {
		return false
	}

	if f.createdAfter != nil && c.creation.Before(*f.createdAfter) {
		return false
	}

	if f.createdBefore != nil && !c.creation.Before(*f.createdBefore) {
		return false
	}

	return true
}

// stepFilter carries the ListSteps state/id predicate.
type stepFilter struct {
	states map[string]bool
	ids    map[string]bool
}

// matches reports whether step st passes the filter.
func (f stepFilter) matches(st *step) bool {
	if len(f.states) > 0 && !f.states[st.state] {
		return false
	}

	if len(f.ids) > 0 && !f.ids[st.id] {
		return false
	}

	return true
}

// instanceFilter carries the ListInstances group/state predicate.
type instanceFilter struct {
	groupID    string
	groupTypes map[string]bool
	states     map[string]bool
}

// matchesGroup reports whether group g passes the group-scoped filter.
func (f instanceFilter) matchesGroup(g *instanceGroup) bool {
	if f.groupID != "" && g.id != f.groupID {
		return false
	}

	if len(f.groupTypes) > 0 && !f.groupTypes[g.groupType] {
		return false
	}

	return true
}

// matchesInstance reports whether instance inst passes the state filter.
func (f instanceFilter) matchesInstance(inst *instance) bool {
	return len(f.states) == 0 || f.states[inst.state]
}

// --- Domain -> wire mapping ---

// toInstanceGroupWire renders an instance group as its ListInstanceGroups wire view.
func toInstanceGroupWire(g *instanceGroup) instanceGroupWire {
	return instanceGroupWire{
		ID:                     g.id,
		Name:                   g.name,
		InstanceGroupType:      g.groupType,
		InstanceType:           g.instanceType,
		Market:                 g.market,
		BidPrice:               g.bidPrice,
		RequestedInstanceCount: g.requested,
		RunningInstanceCount:   g.running,
		Status: statusWire{
			State:    g.state,
			Timeline: &timelineWire{CreationDateTime: epoch(g.created), ReadyDateTime: epoch(g.created)},
		},
	}
}

// toInstanceWire renders an instance as its ListInstances wire view.
func toInstanceWire(inst *instance) instanceWire {
	return instanceWire{
		ID:              inst.id,
		Ec2InstanceID:   inst.ec2InstanceID,
		InstanceGroupID: inst.groupID,
		InstanceType:    inst.instanceType,
		Market:          inst.market,
		PrivateDNSName:  inst.privateDNS,
		PrivateIPAddr:   inst.privateIP,
		Status: statusWire{
			State:    inst.state,
			Timeline: &timelineWire{CreationDateTime: epoch(inst.created), ReadyDateTime: epoch(inst.created)},
		},
	}
}

// toClusterWire renders a cluster as its DescribeCluster wire view.
func toClusterWire(c *cluster) *clusterWire {
	out := &clusterWire{
		ID:                      c.id,
		Name:                    c.name,
		ClusterArn:              c.arn,
		ReleaseLabel:            c.releaseLabel,
		LogURI:                  c.logURI,
		ServiceRole:             c.serviceRole,
		Status:                  toStatusWire(c),
		InstanceCollectionType:  "INSTANCE_GROUP",
		AutoTerminate:           c.autoTerminate,
		TerminationProtected:    c.terminationProtected,
		VisibleToAllUsers:       c.visibleToAll,
		NormalizedInstanceHours: 0,
	}

	if c.ec2SubnetID != "" || c.ec2KeyName != "" {
		out.Ec2InstanceAttributes = &ec2AttributesWire{Ec2SubnetID: c.ec2SubnetID, Ec2KeyName: c.ec2KeyName}
	}

	for _, a := range c.applications {
		out.Applications = append(out.Applications, applicationWire{Name: a.name, Version: a.version, Args: a.args})
	}

	for _, t := range c.tags {
		out.Tags = append(out.Tags, tagWire{Key: t.key, Value: t.value})
	}

	return out
}

// toClusterSummaryWire renders a cluster as its ListClusters wire view.
func toClusterSummaryWire(c *cluster) clusterSummaryWire {
	return clusterSummaryWire{
		ID:                      c.id,
		Name:                    c.name,
		ClusterArn:              c.arn,
		Status:                  toStatusWire(c),
		NormalizedInstanceHours: 0,
	}
}

// toStatusWire renders a cluster's ClusterStatus (state + reason + timeline).
func toStatusWire(c *cluster) statusWire {
	st := statusWire{
		State:    c.state,
		Timeline: &timelineWire{CreationDateTime: epoch(c.creation), ReadyDateTime: epoch(c.ready)},
	}

	if c.end != nil {
		st.Timeline.EndDateTime = epoch(*c.end)
	}

	if c.stateChangeCode != "" || c.stateChangeMessage != "" {
		st.StateChangeReason = &stateChangeReasonWire{Code: c.stateChangeCode, Message: c.stateChangeMessage}
	}

	return st
}

// toStepWire renders a step as its DescribeStep wire view.
func toStepWire(st *step) *stepWire {
	return &stepWire{
		ID:              st.id,
		Name:            st.name,
		Config:          toStepConfigWire(st),
		ActionOnFailure: st.actionOnFailure,
		Status:          toStepStatusWire(st),
	}
}

// toStepSummaryWire renders a step as its ListSteps wire view.
func toStepSummaryWire(st *step) stepSummaryWire {
	return stepSummaryWire{
		ID:              st.id,
		Name:            st.name,
		Config:          toStepConfigWire(st),
		ActionOnFailure: st.actionOnFailure,
		Status:          toStepStatusWire(st),
	}
}

// toStepConfigWire renders a step's HadoopStepConfig, or nil when it carries no jar.
func toStepConfigWire(st *step) *hadoopStepConfigWire {
	if st.jar == "" && st.mainClass == "" && len(st.args) == 0 && len(st.properties) == 0 {
		return nil
	}

	cfg := &hadoopStepConfigWire{Jar: st.jar, MainClass: st.mainClass, Args: st.args}

	if len(st.properties) > 0 {
		cfg.Properties = make(map[string]string, len(st.properties))
		for _, p := range st.properties {
			cfg.Properties[p.key] = p.value
		}
	}

	return cfg
}

// toStepStatusWire renders a step's StepStatus (state + collapsed timeline).
func toStepStatusWire(st *step) stepStatusWire {
	return stepStatusWire{
		State: st.state,
		Timeline: &stepTimelineWire{
			CreationDateTime: epoch(st.submitted),
			StartDateTime:    epoch(st.submitted),
			EndDateTime:      epoch(st.submitted),
		},
	}
}

// --- small helpers ---

// deref returns the pointed-to string, or "" for a nil pointer.
func deref(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}

// derefBool returns the pointed-to bool, or def for a nil pointer.
func derefBool(b *bool, def bool) bool {
	if b == nil {
		return def
	}

	return *b
}

// derefInt32 returns the pointed-to int32, or 0 for a nil pointer.
func derefInt32(v *int32) int32 {
	if v == nil {
		return 0
	}

	return *v
}

// epoch converts a time to a pointer to its epoch-seconds value (the AWS JSON
// default timestamp encoding).
func epoch(t time.Time) *float64 {
	secs := float64(t.UnixNano()) / float64(time.Second)

	return &secs
}
