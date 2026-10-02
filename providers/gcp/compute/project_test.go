package compute

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/internal/projectctx"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
)

func TestDescribeIsScopedToProject(t *testing.T) {
	m := newTestMock()
	bg := context.Background()
	pb := projectctx.WithProject(bg, "p-b")

	if _, err := m.RunInstances(bg, driver.InstanceConfig{ImageID: "img"}, 1); err != nil {
		t.Fatal(err)
	}

	if _, err := m.RunInstances(pb, driver.InstanceConfig{ImageID: "img"}, 1); err != nil {
		t.Fatal(err)
	}

	if _, err := m.CreateVolume(pb, driver.VolumeConfig{Size: 10}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		ctx       context.Context
		wantInst  int
		wantVols  int
		wantOwner string
	}{
		{"unstamped is the default project", bg, 1, 0, "test-project"},
		{"stamped project", pb, 1, 1, "p-b"},
		{"other project", projectctx.WithProject(bg, "p-c"), 0, 0, ""},
		{"all projects", projectctx.AllProjects(bg), 2, 1, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			insts, _ := m.DescribeInstances(tc.ctx, nil, nil)
			vols, _ := m.DescribeVolumes(tc.ctx, nil)

			if len(insts) != tc.wantInst || len(vols) != tc.wantVols {
				t.Fatalf("instances=%d volumes=%d, want %d/%d", len(insts), len(vols), tc.wantInst, tc.wantVols)
			}

			if tc.wantOwner != "" && insts[0].Tags[ProjectTag] != tc.wantOwner {
				t.Errorf("owner tag = %q, want %q", insts[0].Tags[ProjectTag], tc.wantOwner)
			}
		})
	}
}

func TestMIGAndTemplateSameNameTwoProjects(t *testing.T) {
	m := newTestMock()

	for _, p := range []string{"", "p-b"} {
		if err := m.CreateInstanceTemplateGCP(InstanceTemplate{Project: p, Name: "it"}); err != nil {
			t.Fatalf("template in %q: %v", p, err)
		}

		if err := m.CreateInstanceGroupManagerGCP(InstanceGroupManager{
			Project: p, Name: "mig", Zone: "z1", TargetSize: 1, InstanceTemplate: "it",
		}); err != nil {
			t.Fatalf("mig in %q: %v", p, err)
		}
	}

	if err := m.ResizeInstanceGroupManagerGCP("p-b", "z1", "mig", 4); err != nil {
		t.Fatal(err)
	}

	if a, _ := m.GetInstanceGroupManagerGCP("", "z1", "mig"); a.TargetSize != 1 {
		t.Errorf("default-project mig targetSize = %d after resizing p-b's, want 1", a.TargetSize)
	}

	if n := len(m.ListInstanceGroupManagersGCP("p-c", "z1")); n != 0 {
		t.Errorf("p-c lists %d migs, want 0", n)
	}

	if err := m.DeleteInstanceGroupManagerGCP("p-b", "z1", "mig"); err != nil {
		t.Fatal(err)
	}

	if err := m.DeleteInstanceTemplateGCP("p-b", "it"); err != nil {
		t.Errorf("deleting p-b's template blocked by the default project's mig: %v", err)
	}

	if _, ok := m.GetInstanceTemplateGCP("", "it"); !ok {
		t.Error("default-project template gone after deleting p-b's")
	}
}

func TestRestoreAdoptsLegacyRecords(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()
	src.instances.Set("i1", &instanceData{ID: "i1", State: "running", Tags: map[string]string{"cloudemu:gcpName": "vm"}})
	src.volumes.Set("d1", &driver.VolumeInfo{ID: "d1"})
	src.migs.Set("z1/old", InstanceGroupManager{Name: "old", Zone: "z1"})
	src.instTemplates.Set("tpl", InstanceTemplate{Name: "tpl"})

	data, err := src.Snapshot(ctx, false)
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer

	prev, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)

	t.Cleanup(func() { log.SetOutput(prev); log.SetFlags(prevFlags) })

	dst := newTestMock()
	if err := dst.Restore(ctx, data); err != nil {
		t.Fatal(err)
	}

	if got := strings.Count(buf.String(), "gcp/compute: adopted 4 legacy"); got != 1 {
		t.Fatalf("want one adoption warning for 4 records, log: %q", buf.String())
	}

	if insts, _ := dst.DescribeInstances(ctx, nil, nil); len(insts) != 1 {
		t.Errorf("default project sees %d legacy instances, want 1", len(insts))
	}

	if insts, _ := dst.DescribeInstances(projectctx.WithProject(ctx, "p-b"), nil, nil); len(insts) != 0 {
		t.Errorf("p-b sees %d legacy instances, want 0", len(insts))
	}

	if _, ok := dst.GetInstanceGroupManagerGCP("", "z1", "old"); !ok {
		t.Error("legacy mig not adopted into the default project")
	}

	if _, ok := dst.GetInstanceTemplateGCP("", "tpl"); !ok {
		t.Error("legacy template not adopted into the default project")
	}

	again, _ := dst.Snapshot(ctx, false)
	buf.Reset()

	if err := newTestMock().Restore(ctx, again); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(buf.String(), "adopted") {
		t.Errorf("restoring an adopted snapshot warned again: %q", buf.String())
	}
}
