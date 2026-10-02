package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"cloud.google.com/go/artifactregistry/apiv1/artifactregistrypb"
	"cloud.google.com/go/backupdr/apiv1/backupdrpb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// moduleRoot is the repository root relative to this package directory.
const moduleRoot = "../.."

// TestGeneratedTablesUpToDate fails when a committed enums_gen.go differs from
// what the generator renders now, so a stale table fails go test in CI.
func TestGeneratedTablesUpToDate(t *testing.T) {
	for _, s := range specs() {
		want, err := render(s)
		if err != nil {
			t.Fatalf("render %s: %v", s.Dir, err)
		}

		path := filepath.Join(moduleRoot, s.Dir, outputFile)

		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}

		if !bytes.Equal(got, want) {
			t.Errorf("%s is stale; run go generate ./...", path)
		}
	}
}

func walkOrFail(t *testing.T, md protoreflect.MessageDescriptor) map[string]enumInfo {
	t.Helper()

	got, err := walk(md)
	if err != nil {
		t.Fatalf("walk %s: %v", md.FullName(), err)
	}

	return got
}

// TestWalkMatchesPBNameMaps ties the descriptor walk to the generated pb
// X_name maps and checks oneof and map paths.
func TestWalkMatchesPBNameMaps(t *testing.T) {
	repo := walkOrFail(t, (&artifactregistrypb.Repository{}).ProtoReflect().Descriptor())
	vault := walkOrFail(t, (&backupdrpb.BackupVault{}).ProtoReflect().Descriptor())

	tests := []struct {
		name   string
		tables map[string]enumInfo
		path   string
		want   map[int32]string
	}{
		{name: "mode", tables: repo, path: "mode", want: artifactregistrypb.Repository_Mode_name},
		{name: "format", tables: repo, path: "format", want: artifactregistrypb.Repository_Format_name},
		{
			name: "map value", tables: repo, path: "cleanupPolicies.*.action",
			want: artifactregistrypb.CleanupPolicy_Action_name,
		},
		{
			name: "oneof inside map value", tables: repo, path: "cleanupPolicies.*.condition.tagState",
			want: artifactregistrypb.CleanupPolicyCondition_TagState_name,
		},
		{
			name: "oneof message", tables: repo, path: "remoteRepositoryConfig.dockerRepository.publicRepository",
			want: artifactregistrypb.RemoteRepositoryConfig_DockerRepository_PublicRepository_name,
		},
		{
			name: "access restriction", tables: vault, path: "accessRestriction",
			want: backupdrpb.BackupVault_AccessRestriction_name,
		},
		{name: "state", tables: vault, path: "state", want: backupdrpb.BackupVault_State_name},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := tc.tables[tc.path]
			if !ok {
				t.Fatalf("path %q missing from walk", tc.path)
			}

			if !reflect.DeepEqual(got.Names, tc.want) {
				t.Fatalf("%s names=%v want %v", tc.path, got.Names, tc.want)
			}
		})
	}
}

// cyclicMessage builds `message Node { Node child = 1; Kind kind = 2; }`, a
// self-referencing message whose recursion holds an enum.
func cyclicMessage(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()

	fdp := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("cycle.proto"),
		Package: proto.String("test.cycle"),
		Syntax:  proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("Node"),
			Field: []*descriptorpb.FieldDescriptorProto{
				{
					Name: proto.String("child"), Number: proto.Int32(1),
					Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
					Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
					TypeName: proto.String(".test.cycle.Node"), JsonName: proto.String("child"),
				},
				{
					Name: proto.String("kind"), Number: proto.Int32(2),
					Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
					Type:     descriptorpb.FieldDescriptorProto_TYPE_ENUM.Enum(),
					TypeName: proto.String(".test.cycle.Node.Kind"), JsonName: proto.String("kind"),
				},
			},
			EnumType: []*descriptorpb.EnumDescriptorProto{{
				Name:  proto.String("Kind"),
				Value: []*descriptorpb.EnumValueDescriptorProto{{Name: proto.String("KIND_UNSPECIFIED"), Number: proto.Int32(0)}},
			}},
		}},
	}

	fd, err := protodesc.NewFile(fdp, nil)
	if err != nil {
		t.Fatalf("build descriptor: %v", err)
	}

	return fd.Messages().Get(0)
}

// TestWalkFailsOnTruncatedEnum checks that the cycle guard fails generation
// instead of silently dropping an enum-bearing subtree.
func TestWalkFailsOnTruncatedEnum(t *testing.T) {
	_, err := walk(cyclicMessage(t))
	if !errors.Is(err, errTruncated) {
		t.Fatalf("walk err=%v want errTruncated", err)
	}
}
