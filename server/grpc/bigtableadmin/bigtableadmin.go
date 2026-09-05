// Package bigtableadmin adapts the Google Cloud Bigtable Admin gRPC surface
// (google.bigtable.admin.v2 BigtableInstanceAdmin + BigtableTableAdmin, plus
// google.longrunning.Operations) onto the emulator's existing bigtable Admin
// store — the same driver the REST handler (server/gcp/bigtable) delegates to.
//
// It is a protocol adapter only: every RPC converts proto <-> driver types and
// forwards to the store, so there is no second backend and no duplicated
// control-plane logic. Clients that dial gRPC via BIGTABLE_EMULATOR_HOST (the
// cloud.google.com/go/bigtable admin clients and the terraform google provider)
// reach the same in-memory state the REST clients see.
//
// Long-running operations complete synchronously: the store returns done
// operations, so instance/cluster create RPCs return a longrunningpb.Operation
// with Done=true carrying the typed resource in Response, and table create is a
// plain synchronous RPC that returns the Table directly.
package bigtableadmin

import (
	"context"
	"strings"

	adminpb "cloud.google.com/go/bigtable/admin/apiv2/adminpb"
	longrunningpb "cloud.google.com/go/longrunning/autogen/longrunningpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	btdriver "github.com/stackshy/cloudemu/v2/services/bigtable/driver"
)

// AdminFunc resolves the current bigtable Admin store on each call. It is a
// function rather than a fixed reference so the gRPC servers always target the
// live store even after a reset/restore swaps in a fresh provider — mirroring
// how the REST path reads the current handler from its admin.Backend.
type AdminFunc func() btdriver.Admin

// Registrar is the seam the emulator's server/grpc.Server exposes for attaching
// application service servers (its Register method). It matches the contract of
// grpc.ServiceRegistrar under a different method name, so this package can wire
// the generated stubs without depending on the concrete server type.
type Registrar interface {
	Register(desc *grpc.ServiceDesc, impl any)
}

// serviceRegistrar adapts a Registrar to grpc.ServiceRegistrar, which the
// generated RegisterXxxServer helpers require.
type serviceRegistrar struct{ reg Registrar }

func (s serviceRegistrar) RegisterService(desc *grpc.ServiceDesc, impl any) {
	s.reg.Register(desc, impl)
}

// Register attaches the Bigtable instance-admin, table-admin, and long-running
// Operations servers to reg, each delegating to the store returned by resolve.
// It returns the full gRPC service names registered, so the caller can mark
// them SERVING on the health server.
func Register(reg Registrar, resolve AdminFunc) []string {
	sr := serviceRegistrar{reg: reg}

	adminpb.RegisterBigtableInstanceAdminServer(sr, &instanceAdminServer{resolve: resolve})
	adminpb.RegisterBigtableTableAdminServer(sr, &tableAdminServer{resolve: resolve})
	longrunningpb.RegisterOperationsServer(sr, &operationsServer{resolve: resolve})

	return []string{
		adminpb.BigtableInstanceAdmin_ServiceDesc.ServiceName,
		adminpb.BigtableTableAdmin_ServiceDesc.ServiceName,
		longrunningpb.Operations_ServiceDesc.ServiceName,
	}
}

// operationsServer answers google.longrunning.Operations. The store completes
// every operation synchronously, so GetOperation returns a done operation and
// reconstructs the typed response from the target resource for any client that
// still polls (the idiomatic admin clients do not, since create already returns
// a done op with the response embedded).
type operationsServer struct {
	longrunningpb.UnimplementedOperationsServer

	resolve AdminFunc
}

func (s *operationsServer) GetOperation(
	ctx context.Context, req *longrunningpb.GetOperationRequest,
) (*longrunningpb.Operation, error) {
	db := s.resolve()

	op, err := db.GetOperation(ctx, req.GetName())
	if err != nil {
		return nil, toStatus(err)
	}

	out := &longrunningpb.Operation{Name: op.Name, Done: op.Done}

	if op.Done && op.TargetName != "" {
		if resp := reconstructResponse(ctx, db, op); resp != nil {
			if err := setResponse(out, resp); err != nil {
				return nil, err
			}
		}
	}

	return out, nil
}

// reconstructResponse re-fetches the resource an operation targeted so a poll of
// a done operation carries the same typed response the create RPC returned. A
// missing target (already deleted, or an unknown op) yields nil, and GetOperation
// then returns a done operation without a response.
func reconstructResponse(ctx context.Context, db btdriver.Admin, op *btdriver.Operation) proto.Message {
	switch {
	case strings.Contains(op.Type, "instance"):
		if inst, err := db.GetInstance(ctx, op.TargetName); err == nil {
			return toProtoInstance(inst)
		}
	case strings.Contains(op.Type, "cluster"):
		if c, err := db.GetCluster(ctx, op.TargetName); err == nil {
			return toProtoCluster(c)
		}
	case strings.Contains(op.Type, "table"):
		if t, err := db.GetTable(ctx, op.TargetName); err == nil {
			return toProtoTable(t)
		}
	}

	return nil
}

// doneOp builds a completed long-running operation carrying resp as its typed
// response, for the instance/cluster create + partial-update RPCs.
func doneOp(op *btdriver.Operation, resp proto.Message) (*longrunningpb.Operation, error) {
	name := ""
	if op != nil {
		name = op.Name
	}

	out := &longrunningpb.Operation{Name: name, Done: true}
	if err := setResponse(out, resp); err != nil {
		return nil, err
	}

	return out, nil
}

func setResponse(op *longrunningpb.Operation, resp proto.Message) error {
	if resp == nil {
		return nil
	}

	packed, err := anypb.New(resp)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}

	op.Result = &longrunningpb.Operation_Response{Response: packed}

	return nil
}

// grpcCodeFor maps each canonical cloudemu error code to the gRPC status code
// the real Bigtable Admin API returns for the same condition.
//
//nolint:gochecknoglobals // static error-code lookup table
var grpcCodeFor = map[cerrors.Code]codes.Code{
	cerrors.NotFound:           codes.NotFound,
	cerrors.AlreadyExists:      codes.AlreadyExists,
	cerrors.InvalidArgument:    codes.InvalidArgument,
	cerrors.FailedPrecondition: codes.FailedPrecondition,
	cerrors.PermissionDenied:   codes.PermissionDenied,
	cerrors.Throttled:          codes.ResourceExhausted,
	cerrors.ResourceExhausted:  codes.ResourceExhausted,
	cerrors.Unimplemented:      codes.Unimplemented,
	cerrors.Unavailable:        codes.Unavailable,
	cerrors.Internal:           codes.Internal,
}

// toStatus maps a canonical cloudemu error to the matching gRPC status, using
// only the human message (never the internal code name), so SDKs see the same
// codes the real Bigtable Admin API returns. Unmapped codes fall back to
// Internal.
func toStatus(err error) error {
	if err == nil {
		return nil
	}

	code, ok := grpcCodeFor[cerrors.GetCode(err)]
	if !ok {
		code = codes.Internal
	}

	return status.Error(code, cerrors.Message(err))
}
