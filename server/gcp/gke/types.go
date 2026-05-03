package gke

import (
	"encoding/json"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/providers/gcp/gke"
)

// gkeCluster mirrors google.golang.org/api/container/v1.Cluster's wire shape
// for the fields we honor. Anything we don't care about round-trips as zero
// values via the standard json package.
type gkeCluster struct {
	Name              string            `json:"name,omitempty"`
	Description       string            `json:"description,omitempty"`
	Location          string            `json:"location,omitempty"`
	Network           string            `json:"network,omitempty"`
	Subnetwork        string            `json:"subnetwork,omitempty"`
	InitialNodeCount  int64             `json:"initialNodeCount,omitempty"`
	LoggingService    string            `json:"loggingService,omitempty"`
	MonitoringService string            `json:"monitoringService,omitempty"`
	ResourceLabels    map[string]string `json:"resourceLabels,omitempty"`
	NodePools         []gkeNodePool     `json:"nodePools,omitempty"`
	NodeIpv4CIDRSize  int64             `json:"nodeIpv4CidrSize,omitempty"`
	ClusterIpv4Cidr   string            `json:"clusterIpv4Cidr,omitempty"`
	Endpoint          string            `json:"endpoint,omitempty"`
	MasterAuth        *gkeMasterAuth    `json:"masterAuth,omitempty"`
	Status            string            `json:"status,omitempty"`
	SelfLink          string            `json:"selfLink,omitempty"`
	CurrentMasterVer  string            `json:"currentMasterVersion,omitempty"`
	CurrentNodeVer    string            `json:"currentNodeVersion,omitempty"`
	CreateTime        string            `json:"createTime,omitempty"`
}

type gkeMasterAuth struct {
	Username                string `json:"username,omitempty"`
	ClusterCaCertificate    string `json:"clusterCaCertificate,omitempty"`
	ClientCertificate       string `json:"clientCertificate,omitempty"`
	ClientKey               string `json:"clientKey,omitempty"`
	ClientCertificateConfig any    `json:"clientCertificateConfig,omitempty"`
}

type gkeNodePool struct {
	Name             string             `json:"name,omitempty"`
	Version          string             `json:"version,omitempty"`
	InitialNodeCount int64              `json:"initialNodeCount,omitempty"`
	Locations        []string           `json:"locations,omitempty"`
	Config           *gkeNodeConfig     `json:"config,omitempty"`
	Autoscaling      *gkeAutoscaling    `json:"autoscaling,omitempty"`
	Management       *gkeNodeManagement `json:"management,omitempty"`
	Status           string             `json:"status,omitempty"`
	SelfLink         string             `json:"selfLink,omitempty"`
}

type gkeNodeConfig struct {
	MachineType string `json:"machineType,omitempty"`
	DiskSizeGb  int64  `json:"diskSizeGb,omitempty"`
}

type gkeAutoscaling struct {
	Enabled      bool  `json:"enabled,omitempty"`
	MinNodeCount int64 `json:"minNodeCount,omitempty"`
	MaxNodeCount int64 `json:"maxNodeCount,omitempty"`
}

type gkeNodeManagement struct {
	AutoUpgrade bool `json:"autoUpgrade,omitempty"`
	AutoRepair  bool `json:"autoRepair,omitempty"`
}

// Request envelopes — only the fields we read are listed here.

type createClusterReq struct {
	Cluster *gkeCluster `json:"cluster,omitempty"`
}

type updateClusterReq struct {
	Update *struct {
		DesiredNodeVersion       string            `json:"desiredNodeVersion,omitempty"`
		DesiredMasterVersion     string            `json:"desiredMasterVersion,omitempty"`
		DesiredLoggingService    string            `json:"desiredLoggingService,omitempty"`
		DesiredMonitoringService string            `json:"desiredMonitoringService,omitempty"`
		DesiredResourceLabels    map[string]string `json:"desiredResourceLabels,omitempty"`
	} `json:"update,omitempty"`
}

type setLoggingReq struct {
	LoggingService string `json:"loggingService,omitempty"`
}

type setMonitoringReq struct {
	MonitoringService string `json:"monitoringService,omitempty"`
}

type setMasterAuthReq struct {
	Action string `json:"action,omitempty"`
	Update *struct {
		Username string `json:"username,omitempty"`
		Password string `json:"password,omitempty"`
	} `json:"update,omitempty"`
}

type setLegacyAbacReq struct {
	Enabled bool `json:"enabled,omitempty"`
}

type setNetworkPolicyReq struct {
	NetworkPolicy *struct {
		Enabled bool `json:"enabled,omitempty"`
	} `json:"networkPolicy,omitempty"`
}

type setMaintenancePolicyReq struct {
	MaintenancePolicy *struct {
		Window *struct {
			DailyMaintenanceWindow *struct {
				StartTime string `json:"startTime,omitempty"`
			} `json:"dailyMaintenanceWindow,omitempty"`
		} `json:"window,omitempty"`
	} `json:"maintenancePolicy,omitempty"`
}

type setLabelsReq struct {
	ResourceLabels map[string]string `json:"resourceLabels,omitempty"`
}

type createNodePoolReq struct {
	NodePool *gkeNodePool `json:"nodePool,omitempty"`
}

type updateNodePoolReq struct {
	NodeVersion string `json:"nodeVersion,omitempty"`
	MachineType string `json:"machineType,omitempty"`
	DiskSizeGb  int64  `json:"diskSizeGb,omitempty"`
}

type setNodePoolSizeReq struct {
	NodeCount int64 `json:"nodeCount,omitempty"`
}

type setNodePoolAutoscalingReq struct {
	Autoscaling *gkeAutoscaling `json:"autoscaling,omitempty"`
}

type setNodePoolManagementReq struct {
	Management *gkeNodeManagement `json:"management,omitempty"`
}

type listClustersResp struct {
	Clusters []gkeCluster `json:"clusters"`
}

type listNodePoolsResp struct {
	NodePools []gkeNodePool `json:"nodePools"`
}

type listOperationsResp struct {
	Operations []gkeOperation `json:"operations"`
}

type gkeOperation struct {
	Name          string `json:"name,omitempty"`
	OperationType string `json:"operationType,omitempty"`
	Status        string `json:"status,omitempty"`
	Location      string `json:"location,omitempty"`
	TargetLink    string `json:"targetLink,omitempty"`
	StartTime     string `json:"startTime,omitempty"`
	EndTime       string `json:"endTime,omitempty"`
	SelfLink      string `json:"selfLink,omitempty"`
}

// toClusterResource converts a provider Cluster into the wire shape, populating
// the Wave-1 stub Endpoint and CA certificate so SDK consumers see a complete
// envelope. The endpoint hostname encodes that the data-plane is a stub.
func toClusterResource(c *gke.Cluster, project string, pools []gke.NodePool) gkeCluster {
	out := gkeCluster{
		Name:              c.Name,
		Description:       c.Description,
		Location:          c.Location,
		Network:           c.Network,
		Subnetwork:        c.Subnetwork,
		InitialNodeCount:  c.InitialNodeCount,
		LoggingService:    c.LoggingService,
		MonitoringService: c.MonitoringService,
		ResourceLabels:    c.ResourceLabels,
		NodeIpv4CIDRSize:  c.NodeIPv4CIDRSize,
		ClusterIpv4Cidr:   c.ClusterIPv4CIDR,
		Endpoint:          gke.StubEndpoint,
		MasterAuth: &gkeMasterAuth{
			Username:             c.MasterUsername,
			ClusterCaCertificate: gke.StubCACert,
		},
		Status:           c.Status,
		CurrentMasterVer: gke.StubMasterVer,
		CurrentNodeVer:   gke.StubMasterVer,
		SelfLink:         "projects/" + project + "/locations/" + c.Location + "/clusters/" + c.Name,
		CreateTime:       c.CreatedAt.Format("2006-01-02T15:04:05.000Z"),
	}

	for i := range pools {
		out.NodePools = append(out.NodePools, toNodePoolResource(&pools[i], project))
	}

	return out
}

func toNodePoolResource(np *gke.NodePool, project string) gkeNodePool {
	out := gkeNodePool{
		Name:             np.Name,
		Version:          np.Version,
		InitialNodeCount: np.NodeCount,
		Config: &gkeNodeConfig{
			MachineType: np.MachineType,
			DiskSizeGb:  np.DiskSizeGB,
		},
		Management: &gkeNodeManagement{
			AutoUpgrade: np.AutoUpgrade,
			AutoRepair:  np.AutoRepair,
		},
		Status: np.Status,
		SelfLink: "projects/" + project + "/locations/" + np.Location +
			"/clusters/" + np.ClusterName + "/nodePools/" + np.Name,
	}

	if np.AutoscalingOn || np.AutoscalingMin > 0 || np.AutoscalingMax > 0 {
		out.Autoscaling = &gkeAutoscaling{
			Enabled:      np.AutoscalingOn,
			MinNodeCount: np.AutoscalingMin,
			MaxNodeCount: np.AutoscalingMax,
		}
	}

	return out
}

func toOperationResource(op *gke.Operation, project string) gkeOperation {
	return gkeOperation{
		Name:          op.Name,
		OperationType: op.OperationType,
		Status:        op.Status,
		Location:      op.Location,
		TargetLink:    op.TargetLink,
		StartTime:     op.StartTime.Format("2006-01-02T15:04:05.000Z"),
		EndTime:       op.EndTime.Format("2006-01-02T15:04:05.000Z"),
		SelfLink:      "projects/" + project + "/locations/" + op.Location + "/operations/" + op.Name,
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid JSON: "+err.Error())
		return false
	}

	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, reason, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    status,
			"message": msg,
			"status":  reason,
		},
	})
}

func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
	case cerrors.IsAlreadyExists(err):
		writeError(w, http.StatusConflict, "ALREADY_EXISTS", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
	case cerrors.IsFailedPrecondition(err):
		writeError(w, http.StatusConflict, "FAILED_PRECONDITION", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
	}
}
