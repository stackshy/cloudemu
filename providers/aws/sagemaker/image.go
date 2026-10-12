package sagemaker

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

// Image field limits (API reference for CreateImage / CreateImageVersion).
const (
	maxImageDescLen    = 512
	maxImageDisplayLen = 128
	maxBaseImageLen    = 255
	maxClientTokenLen  = 36
	maxAliasLen        = 128
	maxFrameworkLen    = 128
	maxReleaseNotesLen = 255
	maxMountPathLen    = 1024
	maxPosixID         = 65535
	defaultMountPath   = "/home/sagemaker-user"
	defaultUID         = 1000
	defaultGID         = 100
)

var (
	imageNamePattern   = regexp.MustCompile(`^[a-zA-Z0-9]([-.]?[a-zA-Z0-9]){0,62}$`)
	clientTokenPattern = regexp.MustCompile(`^[a-zA-Z0-9-]+$`)
	aliasPattern       = regexp.MustCompile(`^[a-zA-Z0-9\-_.]+$`)
	frameworkPattern   = regexp.MustCompile(`^[a-zA-Z]+ ?\d+\.\d+(\.\d+)?$`)
	displayNamePattern = regexp.MustCompile(`^\S(.*\S)?$`)
	appConfigPattern   = regexp.MustCompile(`^[a-zA-Z0-9](-*[a-zA-Z0-9]){0,62}$`)
	imageEnums         = map[string][]string{ //nolint:gochecknoglobals // static lookup table
		"jobType":        {"TRAINING", "INFERENCE", "NOTEBOOK_KERNEL"},
		"processor":      {"CPU", "GPU"},
		"vendorGuidance": {"NOT_PROVIDED", "STABLE", "TO_BE_ARCHIVED", "ARCHIVED"},
	}
)

func imageVersionKey(image string, version int) string {
	return fmt.Sprintf("%s\x00%08d", image, version)
}

func (m *Mock) imageARN(name string) string { return m.arn("image/" + name) }

func (m *Mock) imageVersionARN(name string, v int) string {
	return m.arn("image-version/" + name + "/" + fmt.Sprint(v))
}

func validImageEnum(field, v string) error {
	if v == "" {
		return nil
	}

	for _, ok := range imageEnums[field] {
		if v == ok {
			return nil
		}
	}

	return errors.Newf(errors.InvalidArgument, "%s %q must be one of %v", field, v, imageEnums[field])
}

func validAlias(a string) error {
	if a == "" || len(a) > maxAliasLen || !aliasPattern.MatchString(a) || a[0] == '.' || a[0] == '-' {
		return errors.Newf(errors.InvalidArgument,
			"alias %q must be 1-%d characters of letters, digits, '-', '_' and '.', not starting with '.' or '-'", a, maxAliasLen)
	}

	return nil
}

// --- Image ---

func cloneImage(in *driver.Image) *driver.Image {
	out := *in

	out.ClientTokens = make(map[string]int, len(in.ClientTokens))
	for k, v := range in.ClientTokens {
		out.ClientTokens[k] = v
	}

	return &out
}

func validRole(role string) error {
	if len(role) < minRoleARNLen || len(role) > maxRoleARNLen || !roleARNPattern.MatchString(role) {
		return errors.Newf(errors.InvalidArgument, "roleArn %q is not a valid IAM role ARN", role)
	}

	return nil
}

func validDisplayName(v string) error {
	if v == "" || len(v) > maxImageDisplayLen || !displayNamePattern.MatchString(v) {
		return errors.Newf(errors.InvalidArgument,
			"displayName must be 1-%d characters without leading or trailing whitespace", maxImageDisplayLen)
	}

	return nil
}

func validImageDesc(v string) error {
	if v == "" || len(v) > maxImageDescLen {
		return errors.Newf(errors.InvalidArgument, "description must be 1-%d characters", maxImageDescLen)
	}

	return nil
}

// CreateImage creates a custom image; versions are added with CreateImageVersion.
//
//nolint:gocritic // large value params mirror the driver interface
func (m *Mock) CreateImage(_ context.Context, spec driver.ImageSpec) (*driver.Image, error) {
	if !imageNamePattern.MatchString(spec.Name) {
		return nil, errors.Newf(errors.InvalidArgument, "imageName %q must be 1-63 letters, digits, '-' and '.', with no consecutive separators", spec.Name) //nolint:lll // SDK-shaped literal
	}

	if err := validRole(spec.RoleARN); err != nil {
		return nil, err
	}

	if spec.DisplayName != "" {
		if err := validDisplayName(spec.DisplayName); err != nil {
			return nil, err
		}
	}

	if spec.Description != "" {
		if err := validImageDesc(spec.Description); err != nil {
			return nil, err
		}
	}

	if err := validateTags(spec.Tags); err != nil {
		return nil, err
	}

	now := m.now()
	arn := m.imageARN(spec.Name)
	img := &driver.Image{
		ImageName: spec.Name, ImageARN: arn, RoleARN: spec.RoleARN, DisplayName: spec.DisplayName, Description: spec.Description,
		Status: driver.ImageStatusCreated, CreationTime: now, LastModifiedTime: now, ClientTokens: map[string]int{},
	}

	if !m.images.SetIfAbsent(spec.Name, img) {
		return nil, driver.InUse("image %q already exists", spec.Name)
	}

	m.setTags(arn, spec.Tags)

	return cloneImage(img), nil
}

func (m *Mock) DescribeImage(_ context.Context, name string) (*driver.Image, error) {
	img, ok := m.images.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "Image %q does not exist", name)
	}

	return cloneImage(img), nil
}

//nolint:dupl,gocritic // per-resource shim mirrors the SDK shape; large value params mirror the driver interface
func (m *Mock) ListImages(_ context.Context, opts driver.ListOptions) (driver.ListPage[driver.Image], error) {
	return listFrom(m.images, listSpec[driver.Image]{
		name:     func(t *driver.Image) string { return t.ImageName },
		created:  func(t *driver.Image) string { return t.CreationTime },
		modified: func(t *driver.Image) string { return t.LastModifiedTime },
		clone:    func(t *driver.Image) driver.Image { return *cloneImage(t) },
	}, opts)
}

// UpdateImage updates an image's display name, description or role. A property
// can be cleared with DeleteProperties but not set and cleared in one call.
//
//nolint:gocyclo // validation branches mirror the service's rules
func (m *Mock) UpdateImage(_ context.Context, up driver.ImageUpdate) (*driver.Image, error) {
	if len(up.DeleteProperties) > 2 { //nolint:mnd // DisplayName and Description are the only deletable properties
		return nil, errors.New(errors.InvalidArgument, "deleteProperties must have at most 2 items")
	}

	clearing := map[string]bool{}

	for _, p := range up.DeleteProperties {
		if p != "DisplayName" && p != "Description" {
			return nil, errors.Newf(errors.InvalidArgument, "deleteProperties may only contain DisplayName and Description, not %q", p)
		}

		clearing[p] = true
	}

	if (clearing["DisplayName"] && up.DisplayName != nil) || (clearing["Description"] && up.Description != nil) {
		return nil, errors.New(errors.InvalidArgument, "a property cannot be both updated and deleted")
	}

	if err := validateImageUpdate(&up); err != nil {
		return nil, err
	}

	m.imageMu.Lock()
	defer m.imageMu.Unlock()

	img, ok := m.images.Get(up.Name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "Image %q does not exist", up.Name)
	}

	updated := cloneImage(img)
	if up.DisplayName != nil {
		updated.DisplayName = *up.DisplayName
	}

	if up.Description != nil {
		updated.Description = *up.Description
	}

	if up.RoleARN != nil {
		updated.RoleARN = *up.RoleARN
	}

	if clearing["DisplayName"] {
		updated.DisplayName = ""
	}

	if clearing["Description"] {
		updated.Description = ""
	}

	updated.LastModifiedTime = m.now()
	m.images.Set(up.Name, updated)

	return cloneImage(updated), nil
}

func validateImageUpdate(up *driver.ImageUpdate) error {
	if up.DisplayName != nil {
		if err := validDisplayName(*up.DisplayName); err != nil {
			return err
		}
	}

	if up.Description != nil {
		if err := validImageDesc(*up.Description); err != nil {
			return err
		}
	}

	if up.RoleARN != nil {
		return validRole(*up.RoleARN)
	}

	return nil
}

// DeleteImage deletes an image together with all of its versions.
func (m *Mock) DeleteImage(_ context.Context, name string) error {
	m.imageMu.Lock()
	defer m.imageMu.Unlock()

	img, ok := m.images.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "Image %q does not exist", name)
	}

	for key, v := range m.imageVersions.All() {
		if v.ImageName == name {
			m.imageVersions.Delete(key)
			m.dropTags(v.ImageVersionARN)
		}
	}

	m.images.Delete(name)
	m.dropTags(img.ImageARN)

	return nil
}

// --- Image version ---

func cloneImageVersion(in *driver.ImageVersion) *driver.ImageVersion {
	out := *in
	out.Aliases = append([]string(nil), in.Aliases...)

	return &out
}

func validateVersionSpec(spec *driver.ImageVersionSpec) error {
	if spec.BaseImage == "" || len(spec.BaseImage) > maxBaseImageLen {
		return errors.Newf(errors.InvalidArgument, "baseImage must be 1-%d characters", maxBaseImageLen)
	}

	if spec.ClientToken == "" || len(spec.ClientToken) > maxClientTokenLen || !clientTokenPattern.MatchString(spec.ClientToken) {
		return errors.Newf(errors.InvalidArgument, "clientToken must be 1-%d characters of letters, digits and '-'", maxClientTokenLen)
	}

	for _, a := range spec.Aliases {
		if err := validAlias(a); err != nil {
			return err
		}
	}

	if len(spec.Aliases) > driver.MaxImageVersionAliases {
		return errors.Newf(errors.InvalidArgument, "aliases must have at most %d items", driver.MaxImageVersionAliases)
	}

	return validateVersionAttrs(spec.JobType, spec.Processor, spec.VendorGuidance, spec.MLFramework, spec.ProgrammingLang, spec.ReleaseNotes)
}

func validateVersionAttrs(jobType, processor, vendor, framework, lang, notes string) error {
	for field, v := range map[string]string{"jobType": jobType, "processor": processor, "vendorGuidance": vendor} {
		if err := validImageEnum(field, v); err != nil {
			return err
		}
	}

	for field, v := range map[string]string{"mlFramework": framework, "programmingLang": lang} {
		if v != "" && (len(v) > maxFrameworkLen || !frameworkPattern.MatchString(v)) {
			return errors.Newf(errors.InvalidArgument, "%s %q is not a valid framework version (for example \"PyTorch 1.13\")", field, v)
		}
	}

	if len(notes) > maxReleaseNotesLen {
		return errors.Newf(errors.InvalidArgument, "releaseNotes must be at most %d characters", maxReleaseNotesLen)
	}

	return nil
}

// aliasHolder returns the version of image that currently holds alias, or 0.
func (m *Mock) aliasHolder(image, alias string) int {
	for _, v := range m.imageVersions.All() {
		if v.ImageName != image {
			continue
		}

		for _, a := range v.Aliases {
			if a == alias {
				return v.Version
			}
		}
	}

	return 0
}

// CreateImageVersion creates the next version of an image. Version numbers only
// grow (a deleted highest version's number is never reused) and a repeated
// ClientToken returns the version it created the first time.
//
//nolint:gocritic // large value params mirror the driver interface
func (m *Mock) CreateImageVersion(_ context.Context, spec driver.ImageVersionSpec) (*driver.ImageVersion, error) {
	if err := validateVersionSpec(&spec); err != nil {
		return nil, err
	}

	m.imageMu.Lock()
	defer m.imageMu.Unlock()

	img, ok := m.images.Get(spec.ImageName)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "Image %q does not exist", spec.ImageName)
	}

	if existing, seen := img.ClientTokens[spec.ClientToken]; seen {
		if v, found := m.imageVersions.Get(imageVersionKey(spec.ImageName, existing)); found {
			return cloneImageVersion(v), nil
		}
	}

	for _, a := range spec.Aliases {
		if holder := m.aliasHolder(spec.ImageName, a); holder != 0 {
			return nil, driver.InUse("alias %q is already used by version %d of image %q", a, holder, spec.ImageName)
		}
	}

	version := img.NextVersion + 1
	now := m.now()
	v := &driver.ImageVersion{
		ImageName: spec.ImageName, ImageARN: img.ImageARN, ImageVersionARN: m.imageVersionARN(spec.ImageName, version),
		Version: version, BaseImage: spec.BaseImage, ContainerImage: spec.BaseImage, Status: driver.ImageVersionStatusCreate,
		Aliases: dedupe(spec.Aliases), Horovod: spec.Horovod, JobType: spec.JobType, MLFramework: spec.MLFramework,
		Processor: spec.Processor, ProgrammingLang: spec.ProgrammingLang, ReleaseNotes: spec.ReleaseNotes,
		VendorGuidance: spec.VendorGuidance, CreationTime: now, LastModifiedTime: now,
	}

	updated := cloneImage(img)
	updated.NextVersion = version
	updated.ClientTokens[spec.ClientToken] = version
	updated.LastModifiedTime = now

	m.imageVersions.Set(imageVersionKey(spec.ImageName, version), v)
	m.images.Set(spec.ImageName, updated)

	return cloneImageVersion(v), nil
}

func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))

	for _, s := range in {
		if !seen[s] {
			seen[s] = true

			out = append(out, s)
		}
	}

	return out
}

// resolveVersion finds the version a reference addresses. Version and Alias
// together must agree; with neither, latest is used when allowLatest is set.
func (m *Mock) resolveVersion(ref driver.ImageVersionRef, allowLatest bool) (*driver.ImageVersion, error) {
	if !m.images.Has(ref.ImageName) {
		return nil, errors.Newf(errors.NotFound, "Image %q does not exist", ref.ImageName)
	}

	version := 0

	switch {
	case ref.Version != nil && ref.Alias != "":
		return nil, errors.New(errors.InvalidArgument, "specify either version or alias, not both")
	case ref.Version != nil:
		version = *ref.Version
	case ref.Alias != "":
		if version = m.aliasHolder(ref.ImageName, ref.Alias); version == 0 {
			return nil, errors.Newf(errors.NotFound, "alias %q not found for image %q", ref.Alias, ref.ImageName)
		}
	case allowLatest:
		version = m.latestVersion(ref.ImageName)
	default:
		return nil, errors.New(errors.InvalidArgument, "specify a version or an alias")
	}

	v, ok := m.imageVersions.Get(imageVersionKey(ref.ImageName, version))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "version %d of image %q not found", version, ref.ImageName)
	}

	return v, nil
}

func (m *Mock) latestVersion(image string) int {
	latest := 0

	for _, v := range m.imageVersions.All() {
		if v.ImageName == image && v.Version > latest {
			latest = v.Version
		}
	}

	return latest
}

func (m *Mock) DescribeImageVersion(_ context.Context, ref driver.ImageVersionRef) (*driver.ImageVersion, error) {
	v, err := m.resolveVersion(ref, true)
	if err != nil {
		return nil, err
	}

	return cloneImageVersion(v), nil
}

//nolint:gocritic,lll // large value params mirror the driver interface; SDK-shaped literal
func (m *Mock) ListImageVersions(_ context.Context, imageName string, opts driver.ListOptions) (driver.ListPage[driver.ImageVersion], error) {
	if !m.images.Has(imageName) {
		return driver.ListPage[driver.ImageVersion]{}, errors.Newf(errors.NotFound, "Image %q does not exist", imageName)
	}

	return listFrom(m.imageVersions, listSpec[driver.ImageVersion]{
		name:     func(t *driver.ImageVersion) string { return fmt.Sprintf("%08d", t.Version) },
		created:  func(t *driver.ImageVersion) string { return t.CreationTime },
		modified: func(t *driver.ImageVersion) string { return t.LastModifiedTime },
		extra:    func(t *driver.ImageVersion, _ map[string]string) bool { return t.ImageName == imageName },
		clone:    func(t *driver.ImageVersion) driver.ImageVersion { return *cloneImageVersion(t) },
	}, opts)
}

// UpdateImageVersion changes a version's aliases and attributes.
//
//nolint:gocritic // large value params mirror the driver interface
func (m *Mock) UpdateImageVersion(_ context.Context, up driver.ImageVersionUpdate) (*driver.ImageVersion, error) {
	for _, a := range up.AliasesToAdd {
		if err := validAlias(a); err != nil {
			return nil, err
		}
	}

	if err := validateVersionAttrs(deref(up.JobType), deref(up.Processor), deref(up.VendorGuidance),
		deref(up.MLFramework), deref(up.ProgrammingLang), deref(up.ReleaseNotes)); err != nil {
		return nil, err
	}

	m.imageMu.Lock()
	defer m.imageMu.Unlock()

	cur, err := m.resolveVersion(up.Ref, false)
	if err != nil {
		return nil, err
	}

	for _, a := range up.AliasesToAdd {
		if holder := m.aliasHolder(cur.ImageName, a); holder != 0 && holder != cur.Version {
			return nil, driver.InUse("alias %q is already used by version %d of image %q", a, holder, cur.ImageName)
		}
	}

	updated := cloneImageVersion(cur)
	applyVersionUpdate(updated, &up)

	if len(updated.Aliases) > driver.MaxImageVersionAliases {
		return nil, errors.Newf(errors.InvalidArgument, "aliases must have at most %d items", driver.MaxImageVersionAliases)
	}

	updated.LastModifiedTime = m.now()
	m.imageVersions.Set(imageVersionKey(cur.ImageName, cur.Version), updated)

	return cloneImageVersion(updated), nil
}

func deref(p *string) string {
	if p == nil {
		return ""
	}

	return *p
}

func applyVersionUpdate(v *driver.ImageVersion, up *driver.ImageVersionUpdate) {
	drop := make(map[string]bool, len(up.AliasesToDelete))
	for _, a := range up.AliasesToDelete {
		drop[a] = true
	}

	kept := make([]string, 0, len(v.Aliases))

	for _, a := range v.Aliases {
		if !drop[a] {
			kept = append(kept, a)
		}
	}

	v.Aliases = dedupe(append(kept, up.AliasesToAdd...))

	if up.Horovod != nil {
		v.Horovod = *up.Horovod
	}

	for _, p := range []struct {
		src *string
		dst *string
	}{
		{up.JobType, &v.JobType}, {up.MLFramework, &v.MLFramework}, {up.Processor, &v.Processor},
		{up.ProgrammingLang, &v.ProgrammingLang}, {up.ReleaseNotes, &v.ReleaseNotes}, {up.VendorGuidance, &v.VendorGuidance},
	} {
		if p.src != nil {
			*p.dst = *p.src
		}
	}
}

// DeleteImageVersion deletes one version (and its aliases). The version number
// is not reused.
func (m *Mock) DeleteImageVersion(_ context.Context, ref driver.ImageVersionRef) error {
	m.imageMu.Lock()
	defer m.imageMu.Unlock()

	v, err := m.resolveVersion(ref, false)
	if err != nil {
		return err
	}

	m.imageVersions.Delete(imageVersionKey(v.ImageName, v.Version))
	m.dropTags(v.ImageVersionARN)

	return nil
}

// ListAliases lists the aliases of one version, or of every version of the image.
//
//nolint:gocritic // large value params mirror the driver interface
func (m *Mock) ListAliases(_ context.Context, ref driver.ImageVersionRef, opts driver.ListOptions) (driver.ListPage[string], error) {
	if !m.images.Has(ref.ImageName) {
		return driver.ListPage[string]{}, errors.Newf(errors.NotFound, "Image %q does not exist", ref.ImageName)
	}

	var aliases []string

	if ref.Version != nil || ref.Alias != "" {
		v, err := m.resolveVersion(ref, false)
		if err != nil {
			return driver.ListPage[string]{}, err
		}

		aliases = append(aliases, v.Aliases...)
	} else {
		for _, v := range m.imageVersions.All() {
			if v.ImageName == ref.ImageName {
				aliases = append(aliases, v.Aliases...)
			}
		}
	}

	sort.Strings(aliases)

	items := make([]*string, 0, len(aliases))
	for i := range aliases {
		items = append(items, &aliases[i])
	}

	return listItems(items, listSpec[string]{name: func(s *string) string { return *s }}, opts)
}

// --- App image config ---

func cloneBody(in *driver.AppImageConfigBody) *driver.AppImageConfigBody {
	if in == nil {
		return nil
	}

	out := *in
	out.KernelSpecs = append([]driver.KernelSpec(nil), in.KernelSpecs...)

	if in.ContainerConfig != nil {
		c := *in.ContainerConfig
		c.Arguments = append([]string(nil), in.ContainerConfig.Arguments...)
		c.Entrypoint = append([]string(nil), in.ContainerConfig.Entrypoint...)
		c.Env = copyStrMap(in.ContainerConfig.Env)
		out.ContainerConfig = &c
	}

	if in.FileSystemConfig != nil {
		f := *in.FileSystemConfig
		f.DefaultUID = copyIntPtr(f.DefaultUID)
		f.DefaultGID = copyIntPtr(f.DefaultGID)
		out.FileSystemConfig = &f
	}

	return &out
}

func cloneAppImageConfig(in *driver.AppImageConfig) *driver.AppImageConfig {
	out := *in
	out.KernelGateway, out.JupyterLab, out.CodeEditor = cloneBody(in.KernelGateway), cloneBody(in.JupyterLab), cloneBody(in.CodeEditor)

	return &out
}

// normalizeBody validates an app image config block and applies the file system
// defaults SageMaker documents for an unset mount path, uid and gid.
//
//nolint:gocyclo // validation branches mirror the service's rules
func normalizeBody(b *driver.AppImageConfigBody, kernelGateway bool) (*driver.AppImageConfigBody, error) {
	if b == nil {
		return nil, nil //nolint:nilnil // an absent block is valid
	}

	out := cloneBody(b)

	if kernelGateway {
		if len(out.KernelSpecs) < 1 || len(out.KernelSpecs) > driver.MaxKernelSpecs {
			return nil, errors.Newf(errors.InvalidArgument, "kernelSpecs must contain between 1 and %d items", driver.MaxKernelSpecs)
		}

		for _, k := range out.KernelSpecs {
			if k.Name == "" {
				return nil, errors.New(errors.InvalidArgument, "kernelSpecs[].name is required")
			}
		}
	}

	if f := out.FileSystemConfig; f != nil { //nolint:nestif // validation mirrors the service's rules
		uid, gid := defaultUID, defaultGID
		if f.DefaultUID != nil {
			uid = *f.DefaultUID
		}

		if f.DefaultGID != nil {
			gid = *f.DefaultGID
		}

		if uid < 0 || uid > maxPosixID || gid < 0 || gid > maxPosixID {
			return nil, errors.Newf(errors.InvalidArgument, "defaultUid and defaultGid must be between 0 and %d", maxPosixID)
		}

		f.DefaultUID, f.DefaultGID = &uid, &gid

		if f.MountPath == "" {
			f.MountPath = defaultMountPath
		}

		if len(f.MountPath) > maxMountPathLen || !strings.HasPrefix(f.MountPath, "/") {
			return nil, errors.Newf(errors.InvalidArgument, "mountPath must start with '/' and be at most %d characters", maxMountPathLen)
		}
	}

	return out, nil
}

func normalizeAppConfig(spec *driver.AppImageConfigSpec) (kg, jl, ce *driver.AppImageConfigBody, err error) {
	if !appConfigPattern.MatchString(spec.Name) {
		return nil, nil, nil, errors.Newf(errors.InvalidArgument, "appImageConfigName %q must be 1-63 letters, digits and hyphens", spec.Name)
	}

	if kg, err = normalizeBody(spec.KernelGateway, true); err != nil {
		return nil, nil, nil, err
	}

	if jl, err = normalizeBody(spec.JupyterLab, false); err != nil {
		return nil, nil, nil, err
	}

	if ce, err = normalizeBody(spec.CodeEditor, false); err != nil {
		return nil, nil, nil, err
	}

	return kg, jl, ce, nil
}

func (m *Mock) CreateAppImageConfig(_ context.Context, spec driver.AppImageConfigSpec) (*driver.AppImageConfig, error) {
	kg, jl, ce, err := normalizeAppConfig(&spec)
	if err != nil {
		return nil, err
	}

	if err = validateTags(spec.Tags); err != nil { //nolint:gocritic // large value params mirror the driver interface
		return nil, err
	}

	now := m.now()
	arn := m.arn("app-image-config/" + spec.Name)
	c := &driver.AppImageConfig{
		Name: spec.Name, ARN: arn, KernelGateway: kg, JupyterLab: jl, CodeEditor: ce, CreationTime: now, LastModifiedTime: now,
	}

	if !m.appImageConfigs.SetIfAbsent(spec.Name, c) {
		return nil, driver.InUse("app image config %q already exists", spec.Name)
	}

	m.setTags(arn, spec.Tags)

	return cloneAppImageConfig(c), nil
}

func (m *Mock) DescribeAppImageConfig(_ context.Context, name string) (*driver.AppImageConfig, error) {
	c, ok := m.appImageConfigs.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "AppImageConfig %q does not exist", name)
	}

	return cloneAppImageConfig(c), nil
}

//nolint:dupl,gocritic // per-resource shim mirrors the SDK shape; large value params mirror the driver interface
func (m *Mock) ListAppImageConfigs(_ context.Context, opts driver.ListOptions) (driver.ListPage[driver.AppImageConfig], error) {
	return listFrom(m.appImageConfigs, listSpec[driver.AppImageConfig]{
		name:     func(t *driver.AppImageConfig) string { return t.Name },
		created:  func(t *driver.AppImageConfig) string { return t.CreationTime },
		modified: func(t *driver.AppImageConfig) string { return t.LastModifiedTime },
		clone:    func(t *driver.AppImageConfig) driver.AppImageConfig { return *cloneAppImageConfig(t) },
	}, opts)
}

// UpdateAppImageConfig replaces the config blocks given in spec; blocks not
// given are kept.
func (m *Mock) UpdateAppImageConfig(_ context.Context, spec driver.AppImageConfigSpec) (*driver.AppImageConfig, error) {
	kg, jl, ce, err := normalizeAppConfig(&spec)
	if err != nil {
		return nil, err
	}

	var out *driver.AppImageConfig

	if !m.appImageConfigs.Update(spec.Name, func(c *driver.AppImageConfig) *driver.AppImageConfig {
		updated := cloneAppImageConfig(c)

		if kg != nil {
			updated.KernelGateway = kg
		}

		if jl != nil {
			updated.JupyterLab = jl
		}

		if ce != nil {
			updated.CodeEditor = ce
		}

		updated.LastModifiedTime = m.now()
		out = updated

		return updated
	}) {
		return nil, errors.Newf(errors.NotFound, "AppImageConfig %q does not exist", spec.Name)
	}

	return cloneAppImageConfig(out), nil
}

func (m *Mock) DeleteAppImageConfig(_ context.Context, name string) error {
	c, ok := m.appImageConfigs.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "AppImageConfig %q does not exist", name)
	}

	m.appImageConfigs.Delete(name)
	m.dropTags(c.ARN)

	return nil
}

func copyIntPtr(in *int) *int {
	if in == nil {
		return nil
	}

	v := *in

	return &v
}
