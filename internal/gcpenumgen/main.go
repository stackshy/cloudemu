// Command gcpenumgen writes the gcpenum.Fields tables that let GCP REST
// handlers accept the numeric enums the gapic REST clients send. It walks each
// root message's proto descriptor and emits <pkg>/enums_gen.go, mapping every
// enum field's JSON path to its enum.
//
// It is the only non-test code that imports the service pb packages. The
// generated tables are plain Go literals, so the cloudemu binary never links a
// proto descriptor to read them.
//
// Run from the module root via `go generate ./...` (see cloudemu.go).
package main

import (
	"bytes"
	"errors"
	"fmt"
	"go/format"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"cloud.google.com/go/artifactregistry/apiv1/artifactregistrypb"
	"cloud.google.com/go/backupdr/apiv1/backupdrpb"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// maxDepth caps the descriptor walk. Real GCP resource messages nest far less.
const maxDepth = 10

// table is one generated variable: a root message walked into Fields.
type table struct {
	Var  string
	Root protoreflect.MessageDescriptor
}

// spec is one generated file: an output package directory and its tables.
type spec struct {
	Dir     string
	Package string
	Tables  []table
}

// specs lists every generated file, relative to the module root.
func specs() []spec {
	return []spec{
		{
			Dir:     "server/gcp/artifactregistry",
			Package: "artifactregistry",
			Tables: []table{
				{Var: "repositoryEnums", Root: (&artifactregistrypb.Repository{}).ProtoReflect().Descriptor()},
			},
		},
		{
			Dir:     "server/gcp/backupdr",
			Package: "backupdr",
			Tables: []table{
				{Var: "vaultEnums", Root: (&backupdrpb.BackupVault{}).ProtoReflect().Descriptor()},
			},
		},
	}
}

// outputFile is the generated file name inside each spec directory.
const outputFile = "enums_gen.go"

// outputMode is the permission for a newly written file. git tracks only the
// executable bit, so the checked-in mode is unaffected.
const outputMode = 0o600

func main() {
	for _, s := range specs() {
		src, err := render(s)
		if err != nil {
			log.Fatalf("gcpenumgen: %s: %v", s.Dir, err)
		}

		if err := os.WriteFile(filepath.Join(s.Dir, outputFile), src, outputMode); err != nil {
			log.Fatalf("gcpenumgen: %v", err)
		}
	}
}

// enumInfo is one enum found by the walk.
type enumInfo struct {
	Type  string
	Names map[int32]string
}

// errTruncated is returned when the cycle guard or the depth cap would drop a
// subtree that holds an enum.
var errTruncated = errors.New("enum subtree truncated")

// walk returns every enum reachable from md keyed by JSON path. Repeated
// fields are transparent, map keys become "*", and google.protobuf messages
// (Struct, Value, Any and the well-known types) are not walked.
func walk(md protoreflect.MessageDescriptor) (map[string]enumInfo, error) {
	out := map[string]enumInfo{}

	if err := walkMessage(md, "", []protoreflect.FullName{md.FullName()}, out); err != nil {
		return nil, err
	}

	return out, nil
}

func walkMessage(
	md protoreflect.MessageDescriptor, path string, stack []protoreflect.FullName, out map[string]enumInfo,
) error {
	fields := md.Fields()

	for i := range fields.Len() {
		if err := walkField(fields.Get(i), path, stack, out); err != nil {
			return err
		}
	}

	return nil
}

func walkField(
	fd protoreflect.FieldDescriptor, path string, stack []protoreflect.FullName, out map[string]enumInfo,
) error {
	p := joinPath(path, fd.JSONName())

	if fd.IsMap() {
		fd = fd.MapValue()
		p = joinPath(p, "*")
	}

	if fd.Enum() != nil {
		out[p] = enumOf(fd.Enum())
		return nil
	}

	if fd.Message() != nil {
		return walkChild(fd.Message(), p, stack, out)
	}

	return nil
}

func walkChild(
	md protoreflect.MessageDescriptor, path string, stack []protoreflect.FullName, out map[string]enumInfo,
) error {
	if strings.HasPrefix(string(md.FullName()), "google.protobuf.") {
		return nil
	}

	if onStack(stack, md.FullName()) || len(stack) >= maxDepth {
		if hasEnum(md, map[protoreflect.FullName]bool{}) {
			return fmt.Errorf("%w: %s at %q", errTruncated, md.FullName(), path)
		}

		return nil
	}

	return walkMessage(md, path, append(stack, md.FullName()), out)
}

func onStack(stack []protoreflect.FullName, name protoreflect.FullName) bool {
	for _, s := range stack {
		if s == name {
			return true
		}
	}

	return false
}

// hasEnum reports whether any enum is reachable from md, skipping
// google.protobuf messages the same way the walk does.
func hasEnum(md protoreflect.MessageDescriptor, seen map[protoreflect.FullName]bool) bool {
	if seen[md.FullName()] || strings.HasPrefix(string(md.FullName()), "google.protobuf.") {
		return false
	}

	seen[md.FullName()] = true

	fields := md.Fields()

	for i := range fields.Len() {
		fd := fields.Get(i)
		if fd.IsMap() {
			fd = fd.MapValue()
		}

		if fd.Enum() != nil || (fd.Message() != nil && hasEnum(fd.Message(), seen)) {
			return true
		}
	}

	return false
}

// enumOf collects an enum's values. With allow_alias the first name for a
// number wins, matching the generated pb X_name maps.
func enumOf(ed protoreflect.EnumDescriptor) enumInfo {
	names := map[int32]string{}
	values := ed.Values()

	for i := range values.Len() {
		v := values.Get(i)
		if _, dup := names[int32(v.Number())]; !dup {
			names[int32(v.Number())] = string(v.Name())
		}
	}

	return enumInfo{Type: string(ed.FullName()), Names: names}
}

func joinPath(path, seg string) string {
	if path == "" {
		return seg
	}

	return path + "." + seg
}

// render produces the formatted enums_gen.go source for s.
func render(s spec) ([]byte, error) {
	var b bytes.Buffer

	b.WriteString("// Code generated by gcpenumgen. DO NOT EDIT.\n\n")
	fmt.Fprintf(&b, "package %s\n\n", s.Package)
	b.WriteString("import \"github.com/stackshy/cloudemu/v2/server/wire/gcpenum\"\n")

	for _, t := range s.Tables {
		enums, err := walk(t.Root)
		if err != nil {
			return nil, err
		}

		writeTable(&b, t, enums)
	}

	src, err := format.Source(b.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format: %w", err)
	}

	return src, nil
}

func writeTable(b *bytes.Buffer, t table, enums map[string]enumInfo) {
	fmt.Fprintf(b, "\n// %s maps each enum field of %s, by JSON path, to its proto enum.\n", t.Var, t.Root.FullName())
	b.WriteString("//\n//nolint:gochecknoglobals // generated immutable proto enum table\n")
	fmt.Fprintf(b, "var %s = gcpenum.Fields{\n", t.Var)

	for _, path := range sortedKeys(enums) {
		e := enums[path]
		fmt.Fprintf(b, "\t%q: {Type: %q, Names: map[int32]string{\n", path, e.Type)

		nums := make([]int32, 0, len(e.Names))
		for n := range e.Names {
			nums = append(nums, n)
		}

		sort.Slice(nums, func(i, j int) bool { return nums[i] < nums[j] })

		for _, n := range nums {
			fmt.Fprintf(b, "\t\t%d: %q,\n", n, e.Names[n])
		}

		b.WriteString("\t}},\n")
	}

	b.WriteString("}\n")
}

func sortedKeys(m map[string]enumInfo) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}
