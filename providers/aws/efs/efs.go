// Package efs provides an in-memory mock implementation of AWS EFS.
package efs

import (
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/efs/driver"
)

// Compile-time check that Mock implements driver.EFS.
var _ driver.EFS = (*Mock)(nil)

// fsData is the full server-side state of a file system. Mount-target and
// access-point maps are populated by later phases; the tag path already reads
// accessPts, and DeleteFileSystem guards on mountTgts.
type fsData struct {
	fs        driver.FileSystem
	policy    string
	backup    string // ENABLED | DISABLED
	mountTgts map[string]*driver.MountTarget
	accessPts map[string]*driver.AccessPoint
	mu        sync.RWMutex
}

// Mock is an in-memory implementation of AWS EFS.
type Mock struct {
	fileSystems *memstore.Store[*fsData]
	// apIndex maps an access-point id to its owning file-system id, so
	// id-scoped tag operations resolve without scanning.
	apIndex *memstore.Store[string]

	opts *config.Options
}

// New creates a new EFS mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		fileSystems: memstore.New[*fsData](),
		apIndex:     memstore.New[string](),
		opts:        opts,
	}
}

func (m *Mock) fsARN(id string) string {
	return idgen.AWSARN("elasticfilesystem", m.opts.Region, m.opts.AccountID, "file-system/"+id)
}

// getFS resolves a file system by id under a read lock check.
func (m *Mock) getFS(id string) (*fsData, bool) {
	return m.fileSystems.Get(id)
}

func copyTags(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}
