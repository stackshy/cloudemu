package containerapps

import (
	"github.com/stackshy/cloudemu/v2/providers/azure/containerapps"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	provisioningSucceeded = "Succeeded"
	armTypeEnvironment    = providerName + "/" + typeEnvironments
	armTypeContainerApp   = providerName + "/" + typeContainerApps
)

// envRequest is the ARM PUT/PATCH body for a managed environment.
type envRequest struct {
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties envReqProps       `json:"properties"`
}

type envReqProps struct {
	AppLogsConfiguration *appLogsConfig `json:"appLogsConfiguration,omitempty"`
}

type appLogsConfig struct {
	Destination string `json:"destination,omitempty"`
}

type envResponse struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties envRespProps      `json:"properties"`
}

type envRespProps struct {
	ProvisioningState    string         `json:"provisioningState"`
	DefaultDomain        string         `json:"defaultDomain,omitempty"`
	StaticIP             string         `json:"staticIp,omitempty"`
	AppLogsConfiguration *appLogsConfig `json:"appLogsConfiguration,omitempty"`
}

// listEnvelope is the ARM {value:[...]} collection response, generic over the
// resource type it carries. nextLink is omitted — the emulator returns a single
// page.
type listEnvelope[R any] struct {
	Value    []R    `json:"value"`
	NextLink string `json:"nextLink,omitempty"`
}

func toEnvResponse(e *containerapps.Environment) envResponse {
	props := envRespProps{
		ProvisioningState: provisioningSucceeded,
		DefaultDomain:     e.DefaultDomain,
		StaticIP:          e.StaticIP,
	}
	if e.AppLogs != nil {
		props.AppLogsConfiguration = &appLogsConfig{Destination: e.AppLogs.Destination}
	}

	return envResponse{
		ID:         e.ARMID(),
		Name:       e.Name,
		Type:       armTypeEnvironment,
		Location:   e.Location,
		Tags:       e.Tags,
		Properties: props,
	}
}

// appRequest is the ARM PUT/PATCH body for a container app.
type appRequest struct {
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties appReqProps       `json:"properties"`
}

type appReqProps struct {
	EnvironmentID        string       `json:"environmentId,omitempty"`
	ManagedEnvironmentID string       `json:"managedEnvironmentId,omitempty"`
	Configuration        *appConfig   `json:"configuration,omitempty"`
	Template             *appTemplate `json:"template,omitempty"`
}

type appConfig struct {
	ActiveRevisionsMode string      `json:"activeRevisionsMode,omitempty"`
	Ingress             *appIngress `json:"ingress,omitempty"`
	Secrets             []appSecret `json:"secrets,omitempty"`
}

type appIngress struct {
	Fqdn          string             `json:"fqdn,omitempty"`
	External      bool               `json:"external,omitempty"`
	TargetPort    int32              `json:"targetPort,omitempty"`
	Transport     string             `json:"transport,omitempty"`
	AllowInsecure bool               `json:"allowInsecure,omitempty"`
	Traffic       []appTrafficWeight `json:"traffic,omitempty"`
}

type appTrafficWeight struct {
	RevisionName   string `json:"revisionName,omitempty"`
	Weight         int32  `json:"weight,omitempty"`
	Label          string `json:"label,omitempty"`
	LatestRevision bool   `json:"latestRevision,omitempty"`
}

type appSecret struct {
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
}

type appTemplate struct {
	RevisionSuffix string         `json:"revisionSuffix,omitempty"`
	Containers     []appContainer `json:"containers,omitempty"`
	Scale          *appScale      `json:"scale,omitempty"`
}

type appContainer struct {
	Name      string        `json:"name,omitempty"`
	Image     string        `json:"image,omitempty"`
	Env       []appEnvVar   `json:"env,omitempty"`
	Resources *appResources `json:"resources,omitempty"`
}

type appEnvVar struct {
	Name      string `json:"name,omitempty"`
	Value     string `json:"value,omitempty"`
	SecretRef string `json:"secretRef,omitempty"`
}

type appResources struct {
	CPU    *float64 `json:"cpu,omitempty"`
	Memory string   `json:"memory,omitempty"`
}

type appScale struct {
	MinReplicas *int32 `json:"minReplicas,omitempty"`
	MaxReplicas *int32 `json:"maxReplicas,omitempty"`
}

type appResponse struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties appRespProps      `json:"properties"`
}

type appRespProps struct {
	ProvisioningState    string       `json:"provisioningState"`
	EnvironmentID        string       `json:"environmentId,omitempty"`
	ManagedEnvironmentID string       `json:"managedEnvironmentId,omitempty"`
	LatestRevisionName   string       `json:"latestRevisionName,omitempty"`
	Configuration        *appConfig   `json:"configuration,omitempty"`
	Template             *appTemplate `json:"template,omitempty"`
}

// toAppInput maps a decoded request onto the provider input. It accepts either
// environmentId or the deprecated managedEnvironmentId, preferring the former.
func toAppInput(req *appRequest) containerapps.AppInput {
	in := containerapps.AppInput{
		Location:      req.Location,
		Tags:          req.Tags,
		EnvironmentID: firstNonEmpty(req.Properties.EnvironmentID, req.Properties.ManagedEnvironmentID),
	}

	if c := req.Properties.Configuration; c != nil {
		in.ActiveRevMode = c.ActiveRevisionsMode
		in.Ingress = toIngressModel(c.Ingress)

		for i := range c.Secrets {
			in.SecretNames = append(in.SecretNames, c.Secrets[i].Name)
		}
	}

	if t := req.Properties.Template; t != nil {
		in.Template = toTemplateModel(t)
	}

	return in
}

func toIngressModel(in *appIngress) *containerapps.Ingress {
	if in == nil {
		return nil
	}

	out := &containerapps.Ingress{
		External:      in.External,
		TargetPort:    in.TargetPort,
		Transport:     in.Transport,
		AllowInsecure: in.AllowInsecure,
	}

	for i := range in.Traffic {
		t := in.Traffic[i]
		out.Traffic = append(out.Traffic, containerapps.TrafficWeight{
			RevisionName: t.RevisionName, Weight: t.Weight, Label: t.Label, LatestRevision: t.LatestRevision,
		})
	}

	return out
}

func toTemplateModel(t *appTemplate) containerapps.Template {
	out := containerapps.Template{RevisionSuffix: t.RevisionSuffix}

	if t.Scale != nil {
		out.Scale = &containerapps.Scale{MinReplicas: t.Scale.MinReplicas, MaxReplicas: t.Scale.MaxReplicas}
	}

	for i := range t.Containers {
		c := t.Containers[i]
		cc := containerapps.Container{Name: c.Name, Image: c.Image}

		for j := range c.Env {
			cc.Env = append(cc.Env, containerapps.EnvVar{
				Name: c.Env[j].Name, Value: c.Env[j].Value, SecretRef: c.Env[j].SecretRef,
			})
		}

		if c.Resources != nil {
			r := &containerapps.ContainerResources{Memory: c.Resources.Memory}
			if c.Resources.CPU != nil {
				r.CPU = *c.Resources.CPU
			}

			cc.Resources = r
		}

		out.Containers = append(out.Containers, cc)
	}

	return out
}

func toAppResponse(a *containerapps.ContainerApp) appResponse {
	return appResponse{
		ID:       a.ARMID(),
		Name:     a.Name,
		Type:     armTypeContainerApp,
		Location: a.Location,
		Tags:     a.Tags,
		Properties: appRespProps{
			ProvisioningState:    provisioningSucceeded,
			EnvironmentID:        a.EnvironmentID,
			ManagedEnvironmentID: a.EnvironmentID,
			LatestRevisionName:   a.LatestRevisionName,
			Configuration:        toConfigResponse(a),
			Template:             toTemplateResponse(&a.Template),
		},
	}
}

func toConfigResponse(a *containerapps.ContainerApp) *appConfig {
	if a.ActiveRevMode == "" && a.Ingress == nil && len(a.SecretNames) == 0 {
		return nil
	}

	cfg := &appConfig{ActiveRevisionsMode: a.ActiveRevMode}

	if a.Ingress != nil {
		cfg.Ingress = &appIngress{
			Fqdn:          a.Fqdn,
			External:      a.Ingress.External,
			TargetPort:    a.Ingress.TargetPort,
			Transport:     a.Ingress.Transport,
			AllowInsecure: a.Ingress.AllowInsecure,
			Traffic:       toTrafficResponse(a.Ingress.Traffic),
		}
	}

	// Real Azure returns secret names only, never values, on a read.
	for _, name := range a.SecretNames {
		cfg.Secrets = append(cfg.Secrets, appSecret{Name: name})
	}

	return cfg
}

func toTemplateResponse(t *containerapps.Template) *appTemplate {
	out := &appTemplate{RevisionSuffix: t.RevisionSuffix}

	if t.Scale != nil {
		out.Scale = &appScale{MinReplicas: t.Scale.MinReplicas, MaxReplicas: t.Scale.MaxReplicas}
	}

	for i := range t.Containers {
		c := t.Containers[i]
		cc := appContainer{Name: c.Name, Image: c.Image}

		for j := range c.Env {
			cc.Env = append(cc.Env, appEnvVar{
				Name: c.Env[j].Name, Value: c.Env[j].Value, SecretRef: c.Env[j].SecretRef,
			})
		}

		if c.Resources != nil {
			cpu := c.Resources.CPU
			cc.Resources = &appResources{CPU: &cpu, Memory: c.Resources.Memory}
		}

		out.Containers = append(out.Containers, cc)
	}

	return out
}

func toTrafficResponse(in []containerapps.TrafficWeight) []appTrafficWeight {
	if len(in) == 0 {
		return nil
	}

	out := make([]appTrafficWeight, 0, len(in))
	for i := range in {
		out = append(out, appTrafficWeight{
			RevisionName:   in[i].RevisionName,
			Weight:         in[i].Weight,
			Label:          in[i].Label,
			LatestRevision: in[i].LatestRevision,
		})
	}

	return out
}

// armTypeRevision is the ARM resource type of a container app's revision.
const armTypeRevision = armTypeContainerApp + "/revisions"

// revisionResponse is the ARM shape of a Microsoft.App containerApps revision.
type revisionResponse struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Properties revisionRespProps `json:"properties"`
}

type revisionRespProps struct {
	CreatedTime       string       `json:"createdTime,omitempty"`
	Active            bool         `json:"active"`
	TrafficWeight     int32        `json:"trafficWeight"`
	Replicas          int32        `json:"replicas"`
	ProvisioningState string       `json:"provisioningState,omitempty"`
	RunningState      string       `json:"runningState,omitempty"`
	HealthState       string       `json:"healthState,omitempty"`
	Fqdn              string       `json:"fqdn,omitempty"`
	Template          *appTemplate `json:"template,omitempty"`
}

// toRevisionResponse projects a stored revision onto its ARM wire shape. The id
// is the parent app's ARM id with a /revisions/{name} suffix, matching the URL
// the armappcontainers SDK addresses the revision by.
func toRevisionResponse(rp *azurearm.ResourcePath, rev *containerapps.Revision) revisionResponse {
	appID := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeContainerApps, rp.ResourceName)

	return revisionResponse{
		ID:   appID + "/" + subResourceRevisions + "/" + rev.Name,
		Name: rev.Name,
		Type: armTypeRevision,
		Properties: revisionRespProps{
			CreatedTime:       rev.CreatedTime,
			Active:            rev.Active,
			TrafficWeight:     rev.TrafficWeight,
			Replicas:          rev.Replicas,
			ProvisioningState: rev.ProvisioningState,
			RunningState:      rev.RunningState,
			HealthState:       rev.HealthState,
			Fqdn:              rev.Fqdn,
			Template:          toTemplateResponse(&rev.Template),
		},
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}

	return b
}
