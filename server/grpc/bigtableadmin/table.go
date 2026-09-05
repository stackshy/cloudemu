package bigtableadmin

import (
	"context"

	adminpb "cloud.google.com/go/bigtable/admin/apiv2/adminpb"
	"google.golang.org/protobuf/types/known/emptypb"

	btdriver "github.com/stackshy/cloudemu/v2/services/bigtable/driver"
)

// tableAdminServer serves google.bigtable.admin.v2.BigtableTableAdmin. Table
// create is synchronous (returns the Table directly). RPCs outside this first
// cut (snapshots, authorized views, backups, consistency tokens) report
// Unimplemented via the embedded generated server.
type tableAdminServer struct {
	adminpb.UnimplementedBigtableTableAdminServer

	resolve AdminFunc
}

func (s *tableAdminServer) CreateTable(ctx context.Context, req *adminpb.CreateTableRequest) (*adminpb.Table, error) {
	cfg := btdriver.CreateTableConfig{Parent: req.GetParent(), TableID: req.GetTableId()}
	if in := req.GetTable(); in != nil {
		cfg.ColumnFamilies = fromProtoColumnFamilies(in.GetColumnFamilies())
		cfg.Granularity = granularityName(in.GetGranularity())
		cfg.DeletionProtection = in.GetDeletionProtection()
	}

	t, err := s.resolve().CreateTable(ctx, cfg)
	if err != nil {
		return nil, toStatus(err)
	}

	return toProtoTable(t), nil
}

func (s *tableAdminServer) GetTable(ctx context.Context, req *adminpb.GetTableRequest) (*adminpb.Table, error) {
	t, err := s.resolve().GetTable(ctx, req.GetName())
	if err != nil {
		return nil, toStatus(err)
	}

	return toProtoTable(t), nil
}

func (s *tableAdminServer) ListTables(ctx context.Context, req *adminpb.ListTablesRequest) (*adminpb.ListTablesResponse, error) {
	tables, err := s.resolve().ListTables(ctx, req.GetParent())
	if err != nil {
		return nil, toStatus(err)
	}

	out := &adminpb.ListTablesResponse{}
	for i := range tables {
		out.Tables = append(out.Tables, toProtoTable(&tables[i]))
	}

	return out, nil
}

func (s *tableAdminServer) DeleteTable(ctx context.Context, req *adminpb.DeleteTableRequest) (*emptypb.Empty, error) {
	if err := s.resolve().DeleteTable(ctx, req.GetName()); err != nil {
		return nil, toStatus(err)
	}

	return &emptypb.Empty{}, nil
}

func (s *tableAdminServer) ModifyColumnFamilies(
	ctx context.Context, req *adminpb.ModifyColumnFamiliesRequest,
) (*adminpb.Table, error) {
	mods := make([]btdriver.ColumnFamilyModification, 0, len(req.GetModifications()))

	for _, m := range req.GetModifications() {
		mod := btdriver.ColumnFamilyModification{ID: m.GetId(), Drop: m.GetDrop()}
		if c := m.GetCreate(); c != nil {
			mod.Create = &btdriver.ColumnFamily{GCRule: fromProtoGCRule(c.GetGcRule())}
		}

		if u := m.GetUpdate(); u != nil {
			mod.Update = &btdriver.ColumnFamily{GCRule: fromProtoGCRule(u.GetGcRule())}
		}

		mods = append(mods, mod)
	}

	t, err := s.resolve().ModifyColumnFamilies(ctx, req.GetName(), mods)
	if err != nil {
		return nil, toStatus(err)
	}

	return toProtoTable(t), nil
}
