package compute

import "context"

// seedShapes fills the shape catalog. Real OCI's ListShapes reports what a
// compartment may launch; CloudEmu offers a fixed slice of the commercial
// realm's catalog, enough that an SDK picking a shape by name finds one.
func (m *Mock) seedShapes() {
	shapes := []Shape{
		{
			Name: "VM.Standard.E4.Flex", ProcessorDescription: "2.55 GHz AMD EPYC 7J13 (Milan)",
			OCPUs: 1, MemoryInGBs: 16, NetworkingBandwidthInGbps: 1, MaxVNICAttachments: 2,
			IsFlexible: true, MinOCPUs: 1, MaxOCPUs: 114, MinMemoryInGBs: 1, MaxMemoryInGBs: 1760,
		},
		{
			Name: "VM.Standard.E5.Flex", ProcessorDescription: "2.7 GHz AMD EPYC 9J14 (Genoa)",
			OCPUs: 1, MemoryInGBs: 12, NetworkingBandwidthInGbps: 1, MaxVNICAttachments: 2,
			IsFlexible: true, MinOCPUs: 1, MaxOCPUs: 94, MinMemoryInGBs: 1, MaxMemoryInGBs: 1049,
		},
		{
			Name: "VM.Standard3.Flex", ProcessorDescription: "2.0 GHz Intel Xeon Platinum 8358 (Ice Lake)",
			OCPUs: 1, MemoryInGBs: 16, NetworkingBandwidthInGbps: 1, MaxVNICAttachments: 2,
			IsFlexible: true, MinOCPUs: 1, MaxOCPUs: 32, MinMemoryInGBs: 1, MaxMemoryInGBs: 512,
		},
		{
			Name: "VM.Standard.A1.Flex", ProcessorDescription: "3.0 GHz Ampere Altra",
			OCPUs: 1, MemoryInGBs: 6, NetworkingBandwidthInGbps: 1, MaxVNICAttachments: 2,
			IsFlexible: true, MinOCPUs: 1, MaxOCPUs: 80, MinMemoryInGBs: 1, MaxMemoryInGBs: 512,
		},
		{
			Name: "VM.Standard2.1", ProcessorDescription: "2.0 GHz Intel Xeon Platinum 8167M (Skylake)",
			OCPUs: 1, MemoryInGBs: 15, NetworkingBandwidthInGbps: 1, MaxVNICAttachments: 2,
		},
		{
			Name: "VM.Standard2.2", ProcessorDescription: "2.0 GHz Intel Xeon Platinum 8167M (Skylake)",
			OCPUs: 2, MemoryInGBs: 30, NetworkingBandwidthInGbps: 2, MaxVNICAttachments: 2,
		},
		{
			Name: "VM.Standard2.4", ProcessorDescription: "2.0 GHz Intel Xeon Platinum 8167M (Skylake)",
			OCPUs: 4, MemoryInGBs: 60, NetworkingBandwidthInGbps: 4, MaxVNICAttachments: 4,
		},
		{
			Name: "VM.Optimized3.Flex", ProcessorDescription: "3.6 GHz Intel Xeon 6354 (Ice Lake)",
			OCPUs: 1, MemoryInGBs: 14, NetworkingBandwidthInGbps: 4, MaxVNICAttachments: 2,
			IsFlexible: true, MinOCPUs: 1, MaxOCPUs: 18, MinMemoryInGBs: 1, MaxMemoryInGBs: 256,
		},
		{
			Name: "BM.Standard.E4.128", ProcessorDescription: "2.55 GHz AMD EPYC 7J13 (Milan)",
			OCPUs: 128, MemoryInGBs: 2048, NetworkingBandwidthInGbps: 100, MaxVNICAttachments: 256,
		},
	}

	for _, s := range shapes {
		m.shapes.Set(s.Name, s)
	}
}

// ListShapes returns the shapes a compartment may launch. Real OCI narrows the
// list by image when imageId is given; CloudEmu's platform images run on every
// shape it publishes, so the parameter is validated and then does not narrow.
func (m *Mock) ListShapes(_ context.Context, imageID string) ([]Shape, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if imageID != "" && !m.images.Has(imageID) {
		return nil, imageNotFound(imageID)
	}

	return m.shapes.SortedValues(), nil
}

// Shape returns one shape by name.
func (m *Mock) Shape(name string) (Shape, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.shapes.Get(name)
}
