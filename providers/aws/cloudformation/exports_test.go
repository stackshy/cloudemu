package cloudformation

import (
	"context"
	"fmt"
	"strings"
	"testing"

	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

const exporterTemplate = `{
	"Resources":{"MyBucket":{"Type":"Test::Bucket","Properties":{"Name":"data-bucket"}}},
	"Outputs":{"BucketName":{"Value":{"Ref":"MyBucket"},"Export":{"Name":"shared-bucket"}}}
}`

const importerTemplate = `{
	"Resources":{"Copy":{"Type":"Test::Bucket","Properties":{
		"Name":{"Fn::Join":["-",[{"Fn::ImportValue":"shared-bucket"},"copy"]]}
	}}}
}`

func createOK(t *testing.T, m *Mock, name, body string) cfn.Stack {
	t.Helper()

	_, err := m.CreateStack(context.Background(), &cfn.CreateStackInput{StackName: name, TemplateBody: body})
	requireNoError(t, err)

	return stackStatus(t, m, name)
}

func deleteOK(t *testing.T, m *Mock, name string) {
	t.Helper()

	requireNoError(t, m.DeleteStack(context.Background(), &cfn.DeleteStackInput{StackName: name}))
}

// lastStatus returns a stack's status by id, which also finds a deleted one.
func lastStatus(t *testing.T, m *Mock, id string) cfn.Stack {
	t.Helper()

	return stackStatus(t, m, id)
}

func TestImportValueResolvesExport(t *testing.T) {
	ctx := context.Background()
	store := newBacking()
	m := newTestMock(store)

	createOK(t, m, "exporter", exporterTemplate)
	st := createOK(t, m, "importer", importerTemplate)
	assertEqual(t, st.Status, cfn.StatusCreateComplete, "importer status")

	if !store.items["data-bucket-copy"] {
		t.Fatalf("imported value not resolved: %v", store.items)
	}

	exports, err := m.ListExports(ctx, "")
	requireNoError(t, err)
	assertEqual(t, len(exports.Exports), 1, "exports")
	assertEqual(t, exports.Exports[0].Name, "shared-bucket", "export name")
	assertEqual(t, exports.Exports[0].Value, "data-bucket", "export value")
	assertEqual(t, exports.Exports[0].ExportingStackID, stackStatus(t, m, "exporter").ID, "exporting stack")

	imports, err := m.ListImports(ctx, &cfn.ListImportsInput{ExportName: "shared-bucket"})
	requireNoError(t, err)
	assertEqual(t, strings.Join(imports.Imports, ","), "importer", "imports")
}

func TestImportValueOfMissingExportRollsBack(t *testing.T) {
	store := newBacking()
	m := newTestMock(store)

	st := createOK(t, m, "importer", importerTemplate)
	assertEqual(t, st.Status, cfn.StatusRollbackComplete, "status")
	assertEqual(t, st.StatusReason, "No export named shared-bucket found. Rollback requested by user.", "reason")
	assertEqual(t, len(store.items), 0, "nothing created")
}

func TestDuplicateExportNameFailsTheStack(t *testing.T) {
	store := newBacking()
	m := newTestMock(store)

	createOK(t, m, "exporter", exporterTemplate)

	st := createOK(t, m, "second", strings.ReplaceAll(exporterTemplate, "data-bucket", "other-bucket"))
	assertEqual(t, st.Status, cfn.StatusRollbackComplete, "status")
	assertEqual(t, st.StatusReason,
		"Export with name shared-bucket is already exported by stack exporter. Rollback requested by user.", "reason")

	if store.items["other-bucket"] {
		t.Fatal("rolled back resource still exists")
	}
}

func TestDeleteExporterInUseEndsDeleteFailed(t *testing.T) {
	ctx := context.Background()
	store := newBacking()
	m := newTestMock(store)

	createOK(t, m, "exporter", exporterTemplate)
	createOK(t, m, "importer", importerTemplate)

	deleteOK(t, m, "exporter")

	st := stackStatus(t, m, "exporter")
	assertEqual(t, st.Status, cfn.StatusDeleteFailed, "status")
	assertEqual(t, st.StatusReason, "Cannot delete export shared-bucket as it is in use by importer", "reason")

	if !store.items["data-bucket"] {
		t.Fatal("exporter resource deleted despite the guard")
	}

	deleteOK(t, m, "importer")
	deleteOK(t, m, "exporter")
	assertEqual(t, lastStatus(t, m, st.ID).Status, cfn.StatusDeleteComplete, "retry status")

	exports, err := m.ListExports(ctx, "")
	requireNoError(t, err)
	assertEqual(t, len(exports.Exports), 0, "exports after delete")
}

func TestUpdateThatDropsOrChangesAnExportInUseRollsBack(t *testing.T) {
	cases := []struct {
		name, body, reason string
	}{
		{
			name:   "removed",
			body:   `{"Resources":{"MyBucket":{"Type":"Test::Bucket","Properties":{"Name":"data-bucket"}}}}`,
			reason: "Export shared-bucket cannot be deleted as it is in use by importer",
		},
		{
			name:   "changed",
			body:   strings.ReplaceAll(exporterTemplate, `{"Ref":"MyBucket"}`, `"literal"`),
			reason: "Export shared-bucket cannot be updated as it is in use by importer",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			m := newTestMock(newBacking())

			createOK(t, m, "exporter", exporterTemplate)
			createOK(t, m, "importer", importerTemplate)

			_, err := m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "exporter", TemplateBody: tc.body})
			requireNoError(t, err)

			st := stackStatus(t, m, "exporter")
			assertEqual(t, st.Status, cfn.StatusUpdateRollbackComplete, "status")
			assertEqual(t, st.StatusReason, tc.reason, "reason")

			exports, err := m.ListExports(ctx, "")
			requireNoError(t, err)
			assertEqual(t, len(exports.Exports), 1, "export kept")
			assertEqual(t, exports.Exports[0].Value, "data-bucket", "export value kept")
		})
	}
}

func TestImporterDroppingTheImportReleasesTheExport(t *testing.T) {
	ctx := context.Background()
	m := newTestMock(newBacking())

	createOK(t, m, "exporter", exporterTemplate)
	createOK(t, m, "importer", importerTemplate)

	_, err := m.UpdateStack(ctx, &cfn.UpdateStackInput{
		StackName:    "importer",
		TemplateBody: `{"Resources":{"Copy":{"Type":"Test::Bucket","Properties":{"Name":"plain"}}}}`,
	})
	requireNoError(t, err)
	assertEqual(t, stackStatus(t, m, "importer").Status, cfn.StatusUpdateComplete, "importer update")

	_, err = m.ListImports(ctx, &cfn.ListImportsInput{ExportName: "shared-bucket"})
	assertErrorContains(t, err, "Export 'shared-bucket' is not imported by any stack.")

	deleteOK(t, m, "exporter")
	assertEqual(t, stackStatus(t, m, "importer").Status, cfn.StatusUpdateComplete, "importer untouched")
}

func TestListImportsOfAnExportNothingImports(t *testing.T) {
	m := newTestMock(newBacking())
	createOK(t, m, "exporter", exporterTemplate)

	_, err := m.ListImports(context.Background(), &cfn.ListImportsInput{ExportName: "shared-bucket"})
	assertErrorContains(t, err, "Export 'shared-bucket' is not imported by any stack.")
}

func TestListExportsPages(t *testing.T) {
	ctx := context.Background()
	m := newTestMock(newBacking())

	var outputs []string
	for i := range 150 {
		outputs = append(outputs, fmt.Sprintf(`"O%d":{"Value":"v%d","Export":{"Name":"e-%03d"}}`, i, i, i))
	}

	body := `{"Resources":{"B":{"Type":"Test::Bucket"}},"Outputs":{` + strings.Join(outputs, ",") + `}}`
	assertEqual(t, createOK(t, m, "many", body).Status, cfn.StatusCreateComplete, "status")

	first, err := m.ListExports(ctx, "")
	requireNoError(t, err)
	assertEqual(t, len(first.Exports), 100, "first page")

	if first.NextToken == "" {
		t.Fatal("first page has no NextToken")
	}

	second, err := m.ListExports(ctx, first.NextToken)
	requireNoError(t, err)
	assertEqual(t, len(second.Exports), 50, "second page")
	assertEqual(t, second.NextToken, "", "last page token")
	assertEqual(t, second.Exports[49].Name, "e-149", "sorted by name")

	_, err = m.ListExports(ctx, "not-a-token")
	assertErrorContains(t, err, "Invalid NextToken")
}

func TestImportValueNameMustNotDependOnResources(t *testing.T) {
	m := newTestMock(newBacking())

	_, err := m.CreateStack(context.Background(), &cfn.CreateStackInput{StackName: "s", TemplateBody: `{"Resources":{
		"A":{"Type":"Test::Bucket"},
		"B":{"Type":"Test::Bucket","Properties":{"Name":{"Fn::ImportValue":{"Ref":"A"}}}}
	}}`})
	assertErrorContains(t, err, "Template error: the attribute in Fn::ImportValue must not depend on any resources")
}

func TestExportGuardsSurviveSnapshotRestore(t *testing.T) {
	ctx := context.Background()
	store := newBacking()
	m := newTestMock(store)

	createOK(t, m, "exporter", exporterTemplate)
	createOK(t, m, "importer", importerTemplate)

	snap, err := m.Snapshot(ctx, false)
	requireNoError(t, err)

	restored := newTestMock(store)
	requireNoError(t, restored.Restore(ctx, snap))

	deleteOK(t, restored, "exporter")
	assertEqual(t, stackStatus(t, restored, "exporter").Status, cfn.StatusDeleteFailed, "restored guard")
}

func assertErrorContains(t *testing.T, err error, want string) {
	t.Helper()

	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want it to contain %q", err, want)
	}
}
