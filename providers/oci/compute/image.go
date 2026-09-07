package compute

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// Portable image states, as the driver's ImageInfo carries them.
const (
	imageAvailable    = "available"
	imageDeregistered = "deregistered"
)

// Launch modes an image supports.
const (
	launchModeParavirtualized = "PARAVIRTUALIZED"
	launchModeNative          = "NATIVE"
)

// defaultImageSizeMBs is the size CloudEmu reports for a platform image.
const defaultImageSizeMBs = 47_694

// Image is OCI's view of a machine image: the attributes the portable
// ImageInfo has no field for.
type Image struct {
	ID                     string
	DisplayName            string
	OperatingSystem        string
	OperatingSystemVersion string
	LaunchMode             string
	SizeInMBs              int
	BaseImageID            string
	CompartmentID          string
	// IsPlatform marks one of OCI's own images, which every compartment sees.
	IsPlatform     bool
	LifecycleState string
	TimeCreated    string
	Tags           map[string]string
}

type imageData struct {
	ID          string
	Name        string
	State       string
	Description string
	CreatedAt   string
	Tags        map[string]string
	OS          string
	OSVersion   string
	LaunchMode  string
	SizeInMBs   int
	BaseImageID string
	InstanceID  string
	Platform    bool
}

// seedImages fills the platform image catalog. Real OCI publishes its own
// images into every compartment; CloudEmu offers a slice of that catalog so
// a launch has an image OCID to name.
func (m *Mock) seedImages() {
	seeds := []struct {
		name, os, version string
	}{
		{"Oracle-Linux-9.4-2024.09.30-0", "Oracle Linux", "9"},
		{"Oracle-Linux-8.10-2024.09.30-0", "Oracle Linux", "8"},
		{"Canonical-Ubuntu-22.04-2024.09.30-0", "Canonical Ubuntu", "22.04"},
		{"Canonical-Ubuntu-24.04-2024.09.30-0", "Canonical Ubuntu", "24.04"},
		{"Windows-Server-2022-Standard-Edition-VM-2024.09.30-0", "Windows", "Server 2022 Standard"},
	}

	for _, s := range seeds {
		id := m.newOCID(typeImage)
		m.images.Set(id, &imageData{
			ID:          id,
			Name:        s.name,
			State:       imageAvailable,
			Description: s.name,
			CreatedAt:   m.now(),
			Tags:        map[string]string{},
			OS:          s.os,
			OSVersion:   s.version,
			LaunchMode:  launchModeParavirtualized,
			SizeInMBs:   defaultImageSizeMBs,
			Platform:    true,
		})
		m.record(id)
	}
}

// CreateImage captures a custom image from an instance.
func (m *Mock) CreateImage(_ context.Context, cfg driver.ImageConfig) (*driver.ImageInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances.Get(cfg.InstanceID)
	if !ok {
		return nil, instanceNotFound(cfg.InstanceID)
	}

	base, _ := m.images.Get(inst.ImageID)
	id := m.newOCID(typeImage)
	img := &imageData{
		ID:          id,
		Name:        cfg.Name,
		State:       imageAvailable,
		Description: cfg.Description,
		CreatedAt:   m.now(),
		Tags:        copyTags(cfg.Tags),
		LaunchMode:  launchModeParavirtualized,
		SizeInMBs:   defaultImageSizeMBs,
		BaseImageID: inst.ImageID,
		InstanceID:  cfg.InstanceID,
	}

	if base != nil {
		img.OS = base.OS
		img.OSVersion = base.OSVersion
	}

	m.images.Set(id, img)
	m.record(id)

	info := toImageInfo(img)

	return &info, nil
}

// DeregisterImage deletes an image. OCI's own platform images cannot be
// deleted.
func (m *Mock) DeregisterImage(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	img, ok := m.images.Get(id)
	if !ok {
		return imageNotFound(id)
	}

	if img.Platform {
		return cerrors.Newf(cerrors.FailedPrecondition, "platform image %q cannot be deleted", id)
	}

	m.images.Delete(id)
	m.forget(id)

	return nil
}

// DescribeImages returns images matching the given OCIDs, or all if empty.
func (m *Mock) DescribeImages(_ context.Context, ids []string) ([]driver.ImageInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeResources(m.images, ids, toImageInfo), nil
}

// GetImage returns OCI's view of one image.
func (m *Mock) GetImage(_ context.Context, id string) (*Image, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	img, ok := m.images.Get(id)
	if !ok {
		return nil, imageNotFound(id)
	}

	out := m.toImage(img)

	return &out, nil
}

// ListImages returns the images visible in a compartment, narrowed by
// operating system and version. OCI's own platform images are visible from
// every compartment, so they are always included.
func (m *Mock) ListImages(_ context.Context, compartmentID, operatingSystem, osVersion string) ([]Image, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]Image, 0)

	for _, img := range m.images.SortedValues() {
		s, _ := m.scopes.Get(img.ID)
		if !img.Platform && s.Compartment != compartmentID {
			continue
		}

		if operatingSystem != "" && img.OS != operatingSystem {
			continue
		}

		if osVersion != "" && img.OSVersion != osVersion {
			continue
		}

		out = append(out, m.toImage(img))
	}

	return out, nil
}

// UpdateImage changes an image's display name and tags.
func (m *Mock) UpdateImage(_ context.Context, id string, upd Update) (*Image, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	img, ok := m.images.Get(id)
	if !ok {
		return nil, imageNotFound(id)
	}

	if img.Platform {
		return nil, cerrors.Newf(cerrors.FailedPrecondition, "platform image %q cannot be updated", id)
	}

	m.images.Update(id, func(v *imageData) *imageData {
		if upd.DisplayName != nil {
			v.Name = *upd.DisplayName
		}

		if upd.Tags != nil {
			v.Tags = mergeTags(v.Tags, upd.Tags)
		}

		return v
	})

	updated, _ := m.images.Get(id)
	out := m.toImage(updated)

	return &out, nil
}

// toImage projects stored image data onto OCI's shape. The caller holds m.mu.
func (m *Mock) toImage(img *imageData) Image {
	s, _ := m.scopes.Get(img.ID)

	return Image{
		ID:                     img.ID,
		DisplayName:            img.Name,
		OperatingSystem:        orDefault(img.OS, "Custom"),
		OperatingSystemVersion: orDefault(img.OSVersion, "Custom"),
		LaunchMode:             orDefault(img.LaunchMode, launchModeNative),
		SizeInMBs:              img.SizeInMBs,
		BaseImageID:            img.BaseImageID,
		CompartmentID:          s.Compartment,
		IsPlatform:             img.Platform,
		LifecycleState:         img.State,
		TimeCreated:            img.CreatedAt,
		Tags:                   copyTags(img.Tags),
	}
}

func toImageInfo(img *imageData) driver.ImageInfo {
	return driver.ImageInfo{
		ID:          img.ID,
		Name:        img.Name,
		State:       img.State,
		Description: img.Description,
		CreatedAt:   img.CreatedAt,
		Tags:        copyTags(img.Tags),
	}
}

func imageNotFound(id string) error {
	return cerrors.Newf(cerrors.NotFound, "image %q not found", id)
}
