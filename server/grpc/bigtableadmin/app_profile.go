package bigtableadmin

import (
	"context"
	"strings"

	adminpb "cloud.google.com/go/bigtable/admin/apiv2/adminpb"
	longrunningpb "cloud.google.com/go/longrunning/autogen/longrunningpb"
	"google.golang.org/protobuf/types/known/emptypb"
)

// Bigtable app profiles on the instance-admin gRPC surface. Create/Get/List and
// Delete are plain synchronous RPCs; UpdateAppProfile is long-running and, like
// the other admin LROs here, completes synchronously (a done Operation carrying
// the updated profile in Response), so the terraform google provider — which
// drives these over BIGTABLE_EMULATOR_HOST — round-trips without hanging.

func (s *instanceAdminServer) CreateAppProfile(
	ctx context.Context, req *adminpb.CreateAppProfileRequest,
) (*adminpb.AppProfile, error) {
	cfg := fromProtoAppProfile(req.GetParent(), req.GetAppProfileId(), req.GetAppProfile())

	a, err := s.resolve().CreateAppProfile(ctx, cfg)
	if err != nil {
		return nil, toStatus(err)
	}

	return toProtoAppProfile(a), nil
}

func (s *instanceAdminServer) GetAppProfile(
	ctx context.Context, req *adminpb.GetAppProfileRequest,
) (*adminpb.AppProfile, error) {
	a, err := s.resolve().GetAppProfile(ctx, req.GetName())
	if err != nil {
		return nil, toStatus(err)
	}

	return toProtoAppProfile(a), nil
}

func (s *instanceAdminServer) ListAppProfiles(
	ctx context.Context, req *adminpb.ListAppProfilesRequest,
) (*adminpb.ListAppProfilesResponse, error) {
	profiles, err := s.resolve().ListAppProfiles(ctx, req.GetParent())
	if err != nil {
		return nil, toStatus(err)
	}

	out := &adminpb.ListAppProfilesResponse{}
	for i := range profiles {
		out.AppProfiles = append(out.AppProfiles, toProtoAppProfile(&profiles[i]))
	}

	return out, nil
}

func (s *instanceAdminServer) UpdateAppProfile(
	ctx context.Context, req *adminpb.UpdateAppProfileRequest,
) (*longrunningpb.Operation, error) {
	db := s.resolve()
	in := req.GetAppProfile()
	name := in.GetName()
	parent, id := splitAppProfileName(name)

	// With no mask the proto contract replaces every field from the body; with a
	// mask, overlay only the named fields onto the current profile so an unmasked
	// routing policy or description is preserved rather than wiped.
	cfg := fromProtoAppProfile(parent, id, in)

	if mask := newMaskSet(req.GetUpdateMask().GetPaths()); mask != nil {
		cur, err := db.GetAppProfile(ctx, name)
		if err != nil {
			return nil, toStatus(err)
		}

		cfg = mergeAppProfileConfig(parent, id, cur, in, mask)
	}

	a, op, err := db.UpdateAppProfile(ctx, name, cfg)
	if err != nil {
		return nil, toStatus(err)
	}

	return doneOp(op, toProtoAppProfile(a))
}

func (s *instanceAdminServer) DeleteAppProfile(
	ctx context.Context, req *adminpb.DeleteAppProfileRequest,
) (*emptypb.Empty, error) {
	if err := s.resolve().DeleteAppProfile(ctx, req.GetName()); err != nil {
		return nil, toStatus(err)
	}

	return &emptypb.Empty{}, nil
}

// splitAppProfileName splits a full app-profile name into its parent instance
// name and profile id: projects/p/instances/i/appProfiles/id ->
// "projects/p/instances/i", "id".
func splitAppProfileName(name string) (parent, id string) {
	const sep = "/appProfiles/"
	if i := strings.Index(name, sep); i >= 0 {
		return name[:i], name[i+len(sep):]
	}

	return name, lastSegment(name)
}
