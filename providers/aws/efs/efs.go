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
	fs          driver.FileSystem
	policy      string
	backup      string // ENABLED | DISABLED
	mountTgts   map[string]*driver.MountTarget
	accessPts   map[string]*driver.AccessPoint
	lifecycle   []driver.LifecyclePolicy
	replication *driver.ReplicationConfiguration
	mu          sync.RWMutex
}

// Mock is an in-memory implementation of AWS EFS.
type Mock struct {
	fileSystems *memstore.Store[*fsData]
	// mtIndex maps a mount-target id, and apIndex an access-point id, to the
	// owning file-system id so id-scoped operations resolve without scanning.
	mtIndex *memstore.Store[string]
	apIndex *memstore.Store[string]
	// tokenIndex maps a creation token to its file-system id, claimed atomically
	// via SetIfAbsent so concurrent same-token CreateFileSystem calls can't both
	// create (EFS idempotency).
	tokenIndex *memstore.Store[string]

	// accountPref is the account-level resource-id preference ("LONG_ID" |
	// "SHORT_ID"); empty until PutAccountPreferences is called.
	accountPref string
	prefMu      sync.RWMutex

	opts *config.Options
}

// New creates a new EFS mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		fileSystems: memstore.New[*fsData](),
		mtIndex:     memstore.New[string](),
		apIndex:     memstore.New[string](),
		tokenIndex:  memstore.New[string](),
		opts:        opts,
	}
}

func (m *Mock) fsARN(id string) string {
	return idgen.AWSARN("elasticfilesystem", m.opts.Region, m.opts.AccountID, "file-system/"+id)
}

func (m *Mock) accessPointARN(id string) string {
	return idgen.AWSARN("elasticfilesystem", m.opts.Region, m.opts.AccountID, "access-point/"+id)
}

// defaultEFSKeyID is the fixed key id used for the account's AWS-managed
// aws/elasticfilesystem CMK, which encrypted file systems default to.
const defaultEFSKeyID = "d7a8f6c0-3b2e-4f1a-9c5d-6e7f80912a34"

// defaultKMSKeyARN returns the ARN of the account's default EFS CMK.
func (m *Mock) defaultKMSKeyARN() string {
	return idgen.AWSARN("kms", m.opts.Region, m.opts.AccountID, "key/"+defaultEFSKeyID)
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
