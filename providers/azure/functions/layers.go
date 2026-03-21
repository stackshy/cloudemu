package functions

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strconv"
	"time"

	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/internal/memstore"
	"github.com/stackshy/cloudemu/serverless/driver"
)

// PublishLayerVersion publishes a new version of a shared code extension.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) PublishLayerVersion(_ context.Context, cfg driver.LayerConfig) (*driver.LayerVersion, error) {
	ld, ok := m.layers.Get(cfg.Name)
	if !ok {
		ld = &layerData{
			versions: memstore.New[*driver.LayerVersion](),
			nextVer:  initialVersion,
		}
		m.layers.Set(cfg.Name, ld)
	}

	ver := ld.nextVer
	ld.nextVer++

	hash := sha256.Sum256(cfg.Content)
	shaStr := fmt.Sprintf("%x", hash)

	arn := idgen.AzureID(
		m.opts.AccountID, "cloudemu-rg", "Microsoft.Web",
		"extensions", cfg.Name+"/versions/"+strconv.Itoa(ver),
	)

	lv := &driver.LayerVersion{
		Name:               cfg.Name,
		Version:            ver,
		Description:        cfg.Description,
		ContentSHA256:      shaStr,
		ContentSize:        int64(len(cfg.Content)),
		CompatibleRuntimes: cfg.CompatibleRuntimes,
		CreatedAt:          time.Now().UTC().Format(time.RFC3339),
		ARN:                arn,
	}

	ld.versions.Set(strconv.Itoa(ver), lv)

	result := *lv

	return &result, nil
}

// GetLayerVersion retrieves a specific version of a shared code extension.
func (m *Mock) GetLayerVersion(_ context.Context, name string, version int) (*driver.LayerVersion, error) {
	ld, ok := m.layers.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "extension %s not found", name)
	}

	lv, ok := ld.versions.Get(strconv.Itoa(version))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "extension %s version %d not found", name, version)
	}

	result := *lv

	return &result, nil
}

// ListLayerVersions returns all versions of a shared code extension.
func (m *Mock) ListLayerVersions(_ context.Context, name string) ([]driver.LayerVersion, error) {
	ld, ok := m.layers.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "extension %s not found", name)
	}

	all := ld.versions.All()
	result := make([]driver.LayerVersion, 0, len(all))

	for _, lv := range all {
		result = append(result, *lv)
	}

	return result, nil
}

// DeleteLayerVersion removes a specific version of a shared code extension.
func (m *Mock) DeleteLayerVersion(_ context.Context, name string, version int) error {
	ld, ok := m.layers.Get(name)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "extension %s not found", name)
	}

	verStr := strconv.Itoa(version)
	if !ld.versions.Has(verStr) {
		return cerrors.Newf(cerrors.NotFound, "extension %s version %d not found", name, version)
	}

	ld.versions.Delete(verStr)

	return nil
}

// ListLayers returns the latest version of each shared code extension.
func (m *Mock) ListLayers(_ context.Context) ([]driver.LayerVersion, error) {
	all := m.layers.All()
	result := make([]driver.LayerVersion, 0, len(all))

	for _, ld := range all {
		latest := findLatestLayerVersion(ld)
		if latest != nil {
			result = append(result, *latest)
		}
	}

	return result, nil
}

// findLatestLayerVersion returns the layer version with the highest version number.
func findLatestLayerVersion(ld *layerData) *driver.LayerVersion {
	all := ld.versions.All()

	var latest *driver.LayerVersion

	for _, lv := range all {
		if latest == nil || lv.Version > latest.Version {
			latest = lv
		}
	}

	return latest
}
