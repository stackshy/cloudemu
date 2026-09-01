package containerapps

import "github.com/stackshy/cloudemu/v2/providers/azure/containerapps"

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
	Fqdn          string `json:"fqdn,omitempty"`
	External      bool   `json:"external,omitempty"`
	TargetPort    int32  `json:"targetPort,omitempty"`
	Transport     string `json:"transport,omitempty"`
	AllowInsecure bool   `json:"allowInsecure,omitempty"`
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

	return &containerapps.Ingress{
		External:      in.External,
		TargetPort:    in.TargetPort,
		Transport:     in.Transport,
		AllowInsecure: in.AllowInsecure,
	}
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

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}

	return b
}
