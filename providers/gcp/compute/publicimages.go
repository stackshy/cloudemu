package compute

import (
	"context"
	"strconv"
	"strings"

	driver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// ImageFamilyTag is the internal tag a user image's family is stored under. The
// wire handler writes it on images.insert; ImageFromFamilyGCP reads it.
const ImageFamilyTag = "cloudemu:gcpImageFamily"

// PublicImage is one entry of the read-only public image catalog: the images
// Google publishes in projects such as debian-cloud. Deprecated marks an older
// image a family has moved past, so getFromFamily skips it.
type PublicImage struct {
	Project     string
	Name        string
	Family      string
	Description string
	DiskSizeGb  int
	CreatedAt   string
	Deprecated  bool
	Replacement string
}

// Public image projects and the build date of their current images.
const (
	debianProject = "debian-cloud"
	debian12      = "debian-12"
	builtAt       = "2025-04-15T00:00:00.000-07:00"

	// Boot disk sizes GCP reports for these images.
	linuxDiskGb   = 10
	rhelDiskGb    = 20
	windowsDiskGb = 50
)

// publicImageCatalog lists, per public project, the newest image of each common
// family plus one deprecated predecessor, with real-style names. Real GCP
// publishes many more; this covers the families Terraform and gcloud examples
// reference.
func publicImageCatalog() []PublicImage {
	img := func(project, name, family, desc string, sizeGb int) PublicImage {
		return PublicImage{Project: project, Name: name, Family: family, Description: desc, DiskSizeGb: sizeGb, CreatedAt: builtAt}
	}

	oldDebian := img(debianProject, "debian-12-bookworm-v20240910", debian12,
		"Debian, Debian GNU/Linux, 12 (bookworm), amd64 built on 20240910", linuxDiskGb)
	oldDebian.CreatedAt = "2024-09-10T00:00:00.000-07:00"
	oldDebian.Deprecated = true
	oldDebian.Replacement = "debian-12-bookworm-v20250415"

	return []PublicImage{
		oldDebian,
		img(debianProject, "debian-12-bookworm-v20250415", debian12,
			"Debian, Debian GNU/Linux, 12 (bookworm), amd64 built on 20250415", linuxDiskGb),
		img(debianProject, "debian-11-bullseye-v20250415", "debian-11",
			"Debian, Debian GNU/Linux, 11 (bullseye), amd64 built on 20250415", linuxDiskGb),
		img("ubuntu-os-cloud", "ubuntu-2204-jammy-v20250415", "ubuntu-2204-lts",
			"Canonical, Ubuntu, 22.04 LTS, amd64 jammy image built on 2025-04-15", linuxDiskGb),
		img("ubuntu-os-cloud", "ubuntu-2404-noble-amd64-v20250415", "ubuntu-2404-lts-amd64",
			"Canonical, Ubuntu, 24.04 LTS, amd64 noble image built on 2025-04-15", linuxDiskGb),
		img("cos-cloud", "cos-stable-117-18613-164-38", "cos-stable",
			"Google, cos-stable-117-18613-164-38, Container-Optimized OS", linuxDiskGb),
		img("rocky-linux-cloud", "rocky-linux-9-v20250415", "rocky-linux-9",
			"Rocky Linux, Rocky Linux, 9, x86_64 built on 20250415", rhelDiskGb),
		img("centos-cloud", "centos-stream-9-v20250415", "centos-stream-9",
			"CentOS, CentOS, Stream 9, x86_64 built on 20250415", rhelDiskGb),
		img("windows-cloud", "windows-server-2022-dc-v20250415", "windows-2022",
			"Microsoft, Windows Server, 2022 Datacenter, x64 built on 20250415", windowsDiskGb),
	}
}

// IsPublicImageProject reports whether project is one of the public image
// projects the catalog serves. Those projects are read-only.
func IsPublicImageProject(project string) bool {
	return len(PublicImages(project)) > 0
}

// PublicImages returns every catalog image in project.
func PublicImages(project string) []PublicImage {
	var out []PublicImage

	for _, img := range publicImageCatalog() {
		if img.Project == project {
			out = append(out, img)
		}
	}

	return out
}

// GetPublicImage returns the catalog image named name in project.
func GetPublicImage(project, name string) (PublicImage, bool) {
	for _, img := range PublicImages(project) {
		if img.Name == name {
			return img, true
		}
	}

	return PublicImage{}, false
}

// PublicImageFromFamily returns the newest non-deprecated catalog image of
// family in project, as images.getFromFamily does.
func PublicImageFromFamily(project, family string) (PublicImage, bool) {
	var (
		best  PublicImage
		found bool
	)

	for _, img := range PublicImages(project) {
		if img.Family != family || img.Deprecated {
			continue
		}

		if !found || img.CreatedAt > best.CreatedAt {
			best, found = img, true
		}
	}

	return best, found
}

// ImageFromFamilyGCP returns the newest image of the project ctx addresses
// whose family is family, as images.getFromFamily does for a project's own
// images. Ties on the creation timestamp (a fake clock) go to the
// later-created image.
func (m *Mock) ImageFromFamilyGCP(ctx context.Context, family string) (driver.ImageInfo, bool) {
	var (
		best  driver.ImageInfo
		found bool
	)

	for _, img := range m.images.All() {
		if family == "" || img.Tags[ImageFamilyTag] != family || !m.visible(ctx, img.Tags) {
			continue
		}

		if !found || newerImage(img, &best) {
			best, found = *img, true
		}
	}

	if found {
		best.Tags = copyTags(best.Tags)
	}

	return best, found
}

// newerImage reports whether a was created after b, falling back to the image
// counter in the ID when the timestamps are equal.
func newerImage(a, b *driver.ImageInfo) bool {
	if a.CreatedAt != b.CreatedAt {
		return a.CreatedAt > b.CreatedAt
	}

	return imageSeq(a.ID) > imageSeq(b.ID)
}

func imageSeq(id string) int {
	i := strings.LastIndex(id, "img-")
	if i < 0 {
		return 0
	}

	n, _ := strconv.Atoi(id[i+len("img-"):])

	return n
}
