package ocm_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/openshift/ocm"
)

func TestOCM_CreateGetListDelete(t *testing.T) {
	m := ocm.New(config.NewOptions())
	ctx := context.Background()

	c, err := m.CreateCluster(ctx, ocm.ClusterInput{Name: "c1", Region: "us-east-1"})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if c.State != "ready" || c.Product != "rosa" || c.Version != "4.16.0" || c.CloudProvider != "aws" {
		t.Errorf("defaults wrong: %+v", c)
	}

	got, err := m.GetCluster(ctx, c.ID)
	if err != nil || got.Name != "c1" {
		t.Fatalf("GetCluster: %v %+v", err, got)
	}

	if list := m.ListClusters(ctx); len(list) != 1 {
		t.Fatalf("ListClusters: got %d, want 1", len(list))
	}

	if err := m.DeleteCluster(ctx, c.ID); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}

	if _, err := m.GetCluster(ctx, c.ID); !cerrors.IsNotFound(err) {
		t.Errorf("GetCluster after delete: want NotFound, got %v", err)
	}
}

func TestOCM_ErrorPaths(t *testing.T) {
	m := ocm.New(config.NewOptions())
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, ocm.ClusterInput{}); !cerrors.IsInvalidArgument(err) {
		t.Errorf("empty name: want InvalidArgument, got %v", err)
	}

	if _, err := m.GetCluster(ctx, "nope"); !cerrors.IsNotFound(err) {
		t.Errorf("get missing: want NotFound, got %v", err)
	}

	if err := m.DeleteCluster(ctx, "nope"); !cerrors.IsNotFound(err) {
		t.Errorf("delete missing: want NotFound, got %v", err)
	}

	// No data plane wired -> Kubeconfig fails precondition (no api server).
	c, _ := m.CreateCluster(ctx, ocm.ClusterInput{Name: "c1"})
	if _, err := m.Kubeconfig(c.ID); err == nil || !strings.Contains(err.Error(), "no data plane") {
		t.Errorf("Kubeconfig without data plane: want failure, got %v", err)
	}
}
