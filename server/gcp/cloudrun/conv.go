package cloudrun

import (
	"strconv"
	"time"

	"github.com/stackshy/cloudemu/v2/services/cloudrun/driver"
)

// jobConfigFromWire maps a wire Job body onto a driver.JobConfig.
func jobConfigFromWire(name string, body *jobResource) driver.JobConfig {
	tt := body.Template.Template

	return driver.JobConfig{
		Name:                 name,
		TaskCount:            body.Template.TaskCount,
		Parallelism:          body.Template.Parallelism,
		MaxRetries:           tt.MaxRetries,
		Timeout:              tt.Timeout,
		ServiceAccount:       tt.ServiceAccount,
		ExecutionEnvironment: tt.ExecutionEnvironment,
		VPCAccess:            toDriverVPC(tt.VPCAccess),
		Containers:           toDriverContainers(tt.Containers),
		Labels:               body.Labels,
		Annotations:          body.Annotations,
	}
}

func toDriverContainers(in []container) []driver.Container {
	out := make([]driver.Container, 0, len(in))
	for i := range in {
		out = append(out, driver.Container{
			Name:      in[i].Name,
			Image:     in[i].Image,
			Command:   in[i].Command,
			Args:      in[i].Args,
			Env:       envToMap(in[i].Env),
			Ports:     toDriverPorts(in[i].Ports),
			Resources: toDriverResources(in[i].Resources),
		})
	}

	return out
}

func toDriverPorts(in []containerPort) []int {
	if len(in) == 0 {
		return nil
	}

	out := make([]int, 0, len(in))
	for _, p := range in {
		out = append(out, p.ContainerPort)
	}

	return out
}

func toWirePorts(in []int) []containerPort {
	if len(in) == 0 {
		return nil
	}

	out := make([]containerPort, 0, len(in))
	for _, p := range in {
		out = append(out, containerPort{ContainerPort: p})
	}

	return out
}

func envToMap(in []envVar) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))
	for _, e := range in {
		out[e.Name] = e.Value
	}

	return out
}

func envToList(in map[string]string) []envVar {
	if len(in) == 0 {
		return nil
	}

	out := make([]envVar, 0, len(in))
	for k, v := range in {
		out = append(out, envVar{Name: k, Value: v})
	}

	return out
}

func toContainers(in []driver.Container) []container {
	out := make([]container, 0, len(in))
	for i := range in {
		out = append(out, container{
			Name:      in[i].Name,
			Image:     in[i].Image,
			Command:   in[i].Command,
			Args:      in[i].Args,
			Env:       envToList(in[i].Env),
			Ports:     toWirePorts(in[i].Ports),
			Resources: toWireResources(in[i].Resources),
		})
	}

	return out
}

func toDriverResources(in *resourceRequirements) *driver.ResourceRequirements {
	if in == nil {
		return nil
	}

	return &driver.ResourceRequirements{Limits: in.Limits, CPUIdle: in.CPUIdle, StartupCPUBoost: in.StartupCPUBoost}
}

func toWireResources(in *driver.ResourceRequirements) *resourceRequirements {
	if in == nil {
		return nil
	}

	return &resourceRequirements{Limits: in.Limits, CPUIdle: in.CPUIdle, StartupCPUBoost: in.StartupCPUBoost}
}

func toDriverVPC(in *vpcAccess) *driver.VpcAccess {
	if in == nil {
		return nil
	}

	out := &driver.VpcAccess{Connector: in.Connector, Egress: in.Egress}
	for _, ni := range in.NetworkInterfaces {
		out.NetworkInterfaces = append(out.NetworkInterfaces, driver.VpcNetworkInterface{
			Network:    ni.Network,
			Subnetwork: ni.Subnetwork,
			Tags:       ni.Tags,
		})
	}

	return out
}

func toWireVPC(in *driver.VpcAccess) *vpcAccess {
	if in == nil {
		return nil
	}

	out := &vpcAccess{Connector: in.Connector, Egress: in.Egress}
	for _, ni := range in.NetworkInterfaces {
		out.NetworkInterfaces = append(out.NetworkInterfaces, networkInterface{
			Network:    ni.Network,
			Subnetwork: ni.Subnetwork,
			Tags:       ni.Tags,
		})
	}

	return out
}

func toConditions(in []driver.Condition) []condition {
	if len(in) == 0 {
		return nil
	}

	out := make([]condition, 0, len(in))
	for _, c := range in {
		out = append(out, condition{Type: c.Type, State: c.State, Message: c.Message, Reason: c.Reason})
	}

	return out
}

func toWireCondition(in *driver.Condition) *condition {
	if in == nil {
		return nil
	}

	return &condition{Type: in.Type, State: in.State, Message: in.Message, Reason: in.Reason}
}

func toJobResource(j *driver.Job, p *crPath) jobResource {
	return jobResource{
		Name:               p.jobName(j.Name),
		UID:                j.UID,
		Generation:         strconv.FormatInt(j.Generation, 10),
		CreateTime:         formatTime(j.CreateTime),
		UpdateTime:         formatTime(j.UpdateTime),
		LaunchStage:        j.LaunchStage,
		ExecutionCount:     j.ExecutionCount,
		Labels:             j.Labels,
		Annotations:        j.Annotations,
		ObservedGeneration: strconv.FormatInt(j.ObservedGeneration, 10),
		TerminalCondition:  toWireCondition(j.TerminalCondition),
		Conditions:         toConditions(j.Conditions),
		Reconciling:        j.Reconciling,
		Etag:               j.Etag,
		Template: execTemplate{
			Parallelism: j.Parallelism,
			TaskCount:   j.TaskCount,
			Template: taskTemplate{
				Containers:           toContainers(j.Containers),
				MaxRetries:           j.MaxRetries,
				Timeout:              j.Timeout,
				ServiceAccount:       j.ServiceAccount,
				ExecutionEnvironment: j.ExecutionEnvironment,
				VPCAccess:            toWireVPC(j.VPCAccess),
			},
		},
		LatestCreatedExecution: toWireExecRef(j.LatestCreatedExecution, p),
	}
}

func toWireExecRef(ref *driver.ExecutionReference, p *crPath) *executionReference {
	if ref == nil {
		return nil
	}

	return &executionReference{
		Name:           p.jobName(execJobID(ref.Name)) + "/executions/" + ref.Name,
		CreateTime:     formatTime(ref.CreateTime),
		CompletionTime: formatTime(ref.CompletionTime),
	}
}

// execJobID recovers the job id from an execution id of the form {job}-{suffix}.
func execJobID(execName string) string {
	if i := lastDash(execName); i >= 0 {
		return execName[:i]
	}

	return execName
}

func lastDash(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '-' {
			return i
		}
	}

	return -1
}

func toExecutionResource(e *driver.Execution, p *crPath) executionResource {
	return executionResource{
		Name:           p.jobName(e.JobName) + "/executions/" + e.Name,
		UID:            e.UID,
		Generation:     strconv.FormatInt(e.Generation, 10),
		Job:            p.jobName(e.JobName),
		CreateTime:     formatTime(e.CreateTime),
		StartTime:      formatTime(e.StartTime),
		CompletionTime: formatTime(e.CompletionTime),
		TaskCount:      e.TaskCount,
		SucceededCount: e.SucceededCount,
		FailedCount:    e.FailedCount,
		RunningCount:   e.RunningCount,
		CancelledCount: e.CancelledCount,
		LogURI:         e.LogURI,
		Template:       taskTemplate{Containers: toContainers(e.Containers)},
		Conditions:     toConditions(e.Conditions),
	}
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	return t.UTC().Format(time.RFC3339Nano)
}
