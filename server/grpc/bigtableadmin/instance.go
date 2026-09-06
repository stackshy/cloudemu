package bigtableadmin

import (
	"context"

	adminpb "cloud.google.com/go/bigtable/admin/apiv2/adminpb"
	iampb "cloud.google.com/go/iam/apiv1/iampb"
	longrunningpb "cloud.google.com/go/longrunning/autogen/longrunningpb"
	"google.golang.org/protobuf/types/known/emptypb"

	btdriver "github.com/stackshy/cloudemu/v2/services/bigtable/driver"
)

// instanceAdminServer serves google.bigtable.admin.v2.BigtableInstanceAdmin,
// forwarding to the current bigtable Admin store. Instances, clusters, app
// profiles (see app_profile.go), and IAM are served here. Embedding the
// generated Unimplemented server keeps forward compatibility: RPCs outside this
// surface (backups, logical/materialized views, hot tablets) report
// Unimplemented rather than failing the whole service.
type instanceAdminServer struct {
	adminpb.UnimplementedBigtableInstanceAdminServer

	resolve AdminFunc
}

func (s *instanceAdminServer) CreateInstance(
	ctx context.Context, req *adminpb.CreateInstanceRequest,
) (*longrunningpb.Operation, error) {
	name := req.GetParent() + "/instances/" + req.GetInstanceId()

	cfg := btdriver.CreateInstanceConfig{Name: name}
	if in := req.GetInstance(); in != nil {
		cfg.DisplayName = in.GetDisplayName()
		cfg.Type = typeName(in.GetType())
		cfg.Labels = in.GetLabels()
	}

	for id, c := range req.GetClusters() {
		cfg.Clusters = append(cfg.Clusters, clusterConfig(name+"/clusters/"+id, c))
	}

	inst, op, err := s.resolve().CreateInstance(ctx, cfg)
	if err != nil {
		return nil, toStatus(err)
	}

	return doneOp(op, toProtoInstance(inst))
}

func (s *instanceAdminServer) GetInstance(ctx context.Context, req *adminpb.GetInstanceRequest) (*adminpb.Instance, error) {
	inst, err := s.resolve().GetInstance(ctx, req.GetName())
	if err != nil {
		return nil, toStatus(err)
	}

	return toProtoInstance(inst), nil
}

func (s *instanceAdminServer) ListInstances(
	ctx context.Context, req *adminpb.ListInstancesRequest,
) (*adminpb.ListInstancesResponse, error) {
	insts, err := s.resolve().ListInstances(ctx, lastSegment(req.GetParent()))
	if err != nil {
		return nil, toStatus(err)
	}

	out := &adminpb.ListInstancesResponse{}
	for i := range insts {
		out.Instances = append(out.Instances, toProtoInstance(&insts[i]))
	}

	return out, nil
}

func (s *instanceAdminServer) UpdateInstance(ctx context.Context, req *adminpb.Instance) (*adminpb.Instance, error) {
	inst, err := s.resolve().UpdateInstance(ctx, req.GetName(), btdriver.UpdateInstanceConfig{
		DisplayName: req.GetDisplayName(), Type: typeName(req.GetType()), Labels: req.GetLabels(),
	})
	if err != nil {
		return nil, toStatus(err)
	}

	return toProtoInstance(inst), nil
}

func (s *instanceAdminServer) PartialUpdateInstance(
	ctx context.Context, req *adminpb.PartialUpdateInstanceRequest,
) (*longrunningpb.Operation, error) {
	in := req.GetInstance()

	cfg := btdriver.UpdateInstanceConfig{
		DisplayName: in.GetDisplayName(), Type: typeName(in.GetType()), Labels: in.GetLabels(),
		UpdateMask: normalizeMaskPaths(req.GetUpdateMask().GetPaths()),
	}

	inst, op, err := s.resolve().PartialUpdateInstance(ctx, in.GetName(), cfg)
	if err != nil {
		return nil, toStatus(err)
	}

	return doneOp(op, toProtoInstance(inst))
}

func (s *instanceAdminServer) DeleteInstance(ctx context.Context, req *adminpb.DeleteInstanceRequest) (*emptypb.Empty, error) {
	if err := s.resolve().DeleteInstance(ctx, req.GetName()); err != nil {
		return nil, toStatus(err)
	}

	return &emptypb.Empty{}, nil
}

// ---- clusters ----

func (s *instanceAdminServer) CreateCluster(
	ctx context.Context, req *adminpb.CreateClusterRequest,
) (*longrunningpb.Operation, error) {
	name := req.GetParent() + "/clusters/" + req.GetClusterId()

	c, op, err := s.resolve().CreateCluster(ctx, clusterConfig(name, req.GetCluster()))
	if err != nil {
		return nil, toStatus(err)
	}

	return doneOp(op, toProtoCluster(c))
}

func (s *instanceAdminServer) GetCluster(ctx context.Context, req *adminpb.GetClusterRequest) (*adminpb.Cluster, error) {
	c, err := s.resolve().GetCluster(ctx, req.GetName())
	if err != nil {
		return nil, toStatus(err)
	}

	return toProtoCluster(c), nil
}

func (s *instanceAdminServer) ListClusters(
	ctx context.Context, req *adminpb.ListClustersRequest,
) (*adminpb.ListClustersResponse, error) {
	clusters, err := s.resolve().ListClusters(ctx, req.GetParent())
	if err != nil {
		return nil, toStatus(err)
	}

	out := &adminpb.ListClustersResponse{}
	for i := range clusters {
		out.Clusters = append(out.Clusters, toProtoCluster(&clusters[i]))
	}

	return out, nil
}

// UpdateCluster is the deprecated full-cluster update (a bare Cluster, no mask):
// the caller's serve_nodes and autoscaling config replace the current scaling.
func (s *instanceAdminServer) UpdateCluster(ctx context.Context, req *adminpb.Cluster) (*longrunningpb.Operation, error) {
	c, op, err := s.resolve().UpdateCluster(ctx, req.GetName(), int(req.GetServeNodes()), fromProtoAutoscaling(req))
	if err != nil {
		return nil, toStatus(err)
	}

	return doneOp(op, toProtoCluster(c))
}

// PartialUpdateCluster applies only the masked scaling fields — this is the RPC
// the terraform google provider uses to change a cluster's node count. An
// unmasked serve_nodes/autoscaling is dropped so the store preserves it rather
// than switching the cluster's scaling mode as a side effect.
func (s *instanceAdminServer) PartialUpdateCluster(
	ctx context.Context, req *adminpb.PartialUpdateClusterRequest,
) (*longrunningpb.Operation, error) {
	in := req.GetCluster()
	serveNodes := int(in.GetServeNodes())
	autoscaling := fromProtoAutoscaling(in)

	if mask := newMaskSet(req.GetUpdateMask().GetPaths()); mask != nil {
		if !mask.has("serveNodes") {
			serveNodes = 0
		}

		if !mask.contains("autoscaling") {
			autoscaling = nil
		}
	}

	c, op, err := s.resolve().UpdateCluster(ctx, in.GetName(), serveNodes, autoscaling)
	if err != nil {
		return nil, toStatus(err)
	}

	return doneOp(op, toProtoCluster(c))
}

func (s *instanceAdminServer) DeleteCluster(ctx context.Context, req *adminpb.DeleteClusterRequest) (*emptypb.Empty, error) {
	if err := s.resolve().DeleteCluster(ctx, req.GetName()); err != nil {
		return nil, toStatus(err)
	}

	return &emptypb.Empty{}, nil
}

// ---- IAM ----

func (s *instanceAdminServer) GetIamPolicy(ctx context.Context, req *iampb.GetIamPolicyRequest) (*iampb.Policy, error) {
	p, err := s.resolve().GetIamPolicy(ctx, req.GetResource())
	if err != nil {
		return nil, toStatus(err)
	}

	return toProtoPolicy(p), nil
}

func (s *instanceAdminServer) SetIamPolicy(ctx context.Context, req *iampb.SetIamPolicyRequest) (*iampb.Policy, error) {
	var pol btdriver.Policy
	if req.GetPolicy() != nil {
		pol = fromProtoPolicy(req.GetPolicy())
	}

	p, err := s.resolve().SetIamPolicy(ctx, req.GetResource(), pol)
	if err != nil {
		return nil, toStatus(err)
	}

	return toProtoPolicy(p), nil
}

func (s *instanceAdminServer) TestIamPermissions(
	ctx context.Context, req *iampb.TestIamPermissionsRequest,
) (*iampb.TestIamPermissionsResponse, error) {
	perms, err := s.resolve().TestIamPermissions(ctx, req.GetResource(), req.GetPermissions())
	if err != nil {
		return nil, toStatus(err)
	}

	return &iampb.TestIamPermissionsResponse{Permissions: perms}, nil
}
