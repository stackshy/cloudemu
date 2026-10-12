package driver

import "context"

// Image and image-version status values (CREATING, CREATE_FAILED, UPDATING and
// DELETING are never observed: the emulator completes these synchronously).
const (
	ImageStatusCreated       = "CREATED"
	ImageVersionStatusCreate = "CREATED"
)

// Image field limits.
const (
	MaxImageVersionAliases = 50
	MaxKernelSpecs         = 5
)

// ImageSpec describes a custom SageMaker image to create.
type ImageSpec struct {
	Name        string
	RoleARN     string
	DisplayName string
	Description string
	Tags        []Tag
}

// Image is a custom SageMaker image. NextVersion and ClientTokens are the
// bookkeeping that keeps version numbers monotonic and creates idempotent.
type Image struct {
	ImageName        string
	ImageARN         string
	RoleARN          string
	DisplayName      string
	Description      string
	Status           string
	FailureReason    string
	CreationTime     string
	LastModifiedTime string
	NextVersion      int
	ClientTokens     map[string]int
}

// ImageUpdate describes an UpdateImage request; a nil field is unchanged and
// DeleteProperties may name DisplayName and/or Description.
type ImageUpdate struct {
	Name             string
	DisplayName      *string
	Description      *string
	RoleARN          *string
	DeleteProperties []string
}

// ImageVersionSpec describes an image version to create.
type ImageVersionSpec struct {
	ImageName       string
	BaseImage       string
	ClientToken     string
	Aliases         []string
	Horovod         bool
	JobType         string
	MLFramework     string
	Processor       string
	ProgrammingLang string
	ReleaseNotes    string
	VendorGuidance  string
}

// ImageVersion is one version of a custom image.
type ImageVersion struct {
	ImageName        string
	ImageARN         string
	ImageVersionARN  string
	Version          int
	BaseImage        string
	ContainerImage   string
	Status           string
	FailureReason    string
	Aliases          []string
	Horovod          bool
	JobType          string
	MLFramework      string
	Processor        string
	ProgrammingLang  string
	ReleaseNotes     string
	VendorGuidance   string
	CreationTime     string
	LastModifiedTime string
}

// ImageVersionRef addresses an image version by number or alias. With neither,
// operations that allow it address the latest version.
type ImageVersionRef struct {
	ImageName string
	Version   *int
	Alias     string
}

// ImageVersionUpdate describes an UpdateImageVersion request.
type ImageVersionUpdate struct {
	Ref             ImageVersionRef
	AliasesToAdd    []string
	AliasesToDelete []string
	Horovod         *bool
	JobType         *string
	MLFramework     *string
	Processor       *string
	ProgrammingLang *string
	ReleaseNotes    *string
	VendorGuidance  *string
}

// FileSystemConfig is the file system of an app image.
type FileSystemConfig struct {
	MountPath  string
	DefaultUID int
	DefaultGID int
}

// KernelSpec names a kernel an app image provides.
type KernelSpec struct {
	Name        string
	DisplayName string
}

// ContainerConfig overrides how an app image's container starts.
type ContainerConfig struct {
	Arguments  []string
	Entrypoint []string
	Env        map[string]string
}

// AppImageConfigBody is the per-app-type part of an app image config.
type AppImageConfigBody struct {
	KernelSpecs      []KernelSpec     // KernelGateway only
	ContainerConfig  *ContainerConfig // JupyterLab and CodeEditor only
	FileSystemConfig *FileSystemConfig
}

// AppImageConfigSpec describes an app image configuration to create.
type AppImageConfigSpec struct {
	Name          string
	KernelGateway *AppImageConfigBody
	JupyterLab    *AppImageConfigBody
	CodeEditor    *AppImageConfigBody
	Tags          []Tag
}

// AppImageConfig is a custom app image configuration.
type AppImageConfig struct {
	Name             string
	ARN              string
	KernelGateway    *AppImageConfigBody
	JupyterLab       *AppImageConfigBody
	CodeEditor       *AppImageConfigBody
	CreationTime     string
	LastModifiedTime string
}

// imageAPI covers Studio custom images, image versions and app image configs.
type imageAPI interface {
	CreateImage(ctx context.Context, spec ImageSpec) (*Image, error)
	DescribeImage(ctx context.Context, name string) (*Image, error)
	ListImages(ctx context.Context, opts ListOptions) (ListPage[Image], error)
	UpdateImage(ctx context.Context, update ImageUpdate) (*Image, error)
	DeleteImage(ctx context.Context, name string) error

	CreateImageVersion(ctx context.Context, spec ImageVersionSpec) (*ImageVersion, error)
	DescribeImageVersion(ctx context.Context, ref ImageVersionRef) (*ImageVersion, error)
	ListImageVersions(ctx context.Context, imageName string, opts ListOptions) (ListPage[ImageVersion], error)
	UpdateImageVersion(ctx context.Context, update ImageVersionUpdate) (*ImageVersion, error)
	DeleteImageVersion(ctx context.Context, ref ImageVersionRef) error
	ListAliases(ctx context.Context, ref ImageVersionRef, opts ListOptions) (ListPage[string], error)

	CreateAppImageConfig(ctx context.Context, spec AppImageConfigSpec) (*AppImageConfig, error)
	DescribeAppImageConfig(ctx context.Context, name string) (*AppImageConfig, error)
	ListAppImageConfigs(ctx context.Context, opts ListOptions) (ListPage[AppImageConfig], error)
	UpdateAppImageConfig(ctx context.Context, spec AppImageConfigSpec) (*AppImageConfig, error)
	DeleteAppImageConfig(ctx context.Context, name string) error
}
