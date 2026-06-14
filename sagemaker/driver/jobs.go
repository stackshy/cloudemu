package driver

import "context"

// Job status values shared across the asynchronous job resources.
const (
	JobInProgress = "InProgress"
	JobCompleted  = "Completed"
	JobFailed     = "Failed"
	JobStopping   = "Stopping"
	JobStopped    = "Stopped"
)

// Training secondary-status values (a subset of the documented set) used to
// synthesize a plausible SecondaryStatusTransitions history.
const (
	SecondaryStarting    = "Starting"
	SecondaryDownloading = "Downloading"
	SecondaryTraining    = "Training"
	SecondaryUploading   = "Uploading"
	SecondaryCompleted   = "Completed"
)

// HPO and labeling carry their own initial states.
const (
	LabelingInitializing = "Initializing"
)

// Compilation (Neo) jobs use upper-case status values, distinct from the
// mixed-case set the other jobs share.
const (
	CompilationInProgress = "INPROGRESS"
	CompilationCompleted  = "COMPLETED"
	CompilationFailed     = "FAILED"
	CompilationStarting   = "STARTING"
	CompilationStopping   = "STOPPING"
	CompilationStopped    = "STOPPED"
)

// Channel is an input data channel for a training/processing job.
type Channel struct {
	Name        string
	S3URI       string
	ContentType string
}

// ResourceConfig describes the compute for a job.
type ResourceConfig struct {
	InstanceType   string
	InstanceCount  int
	VolumeSizeInGB int
}

// TrainingJobConfig describes a training job to create.
type TrainingJobConfig struct {
	JobName           string
	RoleARN           string
	AlgorithmImage    string
	HyperParameters   map[string]string
	InputChannels     []Channel
	OutputS3URI       string
	Resources         ResourceConfig
	MaxRuntimeSeconds int
	Tags              []Tag
}

// SecondaryStatusTransition is one entry in a training job's status history.
type SecondaryStatusTransition struct {
	Status        string
	StartTime     string
	EndTime       string
	StatusMessage string
}

// TrainingJob describes a training job.
type TrainingJob struct {
	JobName              string
	JobARN               string
	RoleARN              string
	AlgorithmImage       string
	HyperParameters      map[string]string
	InputChannels        []Channel
	OutputS3URI          string
	Resources            ResourceConfig
	Status               string
	SecondaryStatus      string
	SecondaryTransitions []SecondaryStatusTransition
	ModelArtifactS3URI   string
	FailureReason        string
	CreationTime         string
	TrainingStartTime    string
	TrainingEndTime      string
	LastModifiedTime     string
	Tags                 []Tag
}

// ProcessingJobConfig describes a processing job to create.
type ProcessingJobConfig struct {
	JobName     string
	RoleARN     string
	AppImage    string
	Inputs      []Channel
	OutputS3URI string
	Resources   ResourceConfig
	Tags        []Tag
}

// ProcessingJob describes a processing job.
type ProcessingJob struct {
	JobName           string
	JobARN            string
	RoleARN           string
	AppImage          string
	Inputs            []Channel
	OutputS3URI       string
	Resources         ResourceConfig
	Status            string
	FailureReason     string
	CreationTime      string
	ProcessingEndTime string
	LastModifiedTime  string
	Tags              []Tag
}

// TransformJobConfig describes a batch transform job to create.
type TransformJobConfig struct {
	JobName       string
	ModelName     string
	InputS3URI    string
	OutputS3URI   string
	InstanceType  string
	InstanceCount int
	Tags          []Tag
}

// TransformJob describes a batch transform job.
type TransformJob struct {
	JobName          string
	JobARN           string
	ModelName        string
	InputS3URI       string
	OutputS3URI      string
	InstanceType     string
	InstanceCount    int
	Status           string
	FailureReason    string
	CreationTime     string
	TransformEndTime string
	LastModifiedTime string
	Tags             []Tag
}

// HyperParameterTuningJobConfig describes a tuning job to create.
type HyperParameterTuningJobConfig struct {
	JobName            string
	Strategy           string // Bayesian, Random, Grid
	MaxJobs            int
	MaxParallelJobs    int
	ObjectiveMetric    string
	ObjectiveType      string // Maximize, Minimize
	TrainingDefinition TrainingJobConfig
	Tags               []Tag
}

// HyperParameterTuningJob describes a tuning job.
type HyperParameterTuningJob struct {
	JobName           string
	JobARN            string
	Strategy          string
	MaxJobs           int
	MaxParallelJobs   int
	ObjectiveMetric   string
	ObjectiveType     string
	Status            string
	BestTrainingJob   string
	TrainingJobCounts map[string]int
	FailureReason     string
	CreationTime      string
	HPOJobEndTime     string
	LastModifiedTime  string
	Tags              []Tag
}

// AutoMLJobConfig describes an Autopilot (AutoML V2) job to create.
type AutoMLJobConfig struct {
	JobName      string
	RoleARN      string
	InputS3URI   string
	OutputS3URI  string
	ProblemType  string // BinaryClassification, MulticlassClassification, Regression
	TargetColumn string
	Tags         []Tag
}

// AutoMLJob describes an AutoML job.
type AutoMLJob struct {
	JobName           string
	JobARN            string
	RoleARN           string
	InputS3URI        string
	OutputS3URI       string
	ProblemType       string
	TargetColumn      string
	Status            string
	SecondaryStatus   string
	BestCandidateName string
	FailureReason     string
	CreationTime      string
	AutoMLJobEndTime  string
	LastModifiedTime  string
	Tags              []Tag
}

// LabelingJobConfig describes a Ground Truth labeling job to create.
type LabelingJobConfig struct {
	JobName        string
	RoleARN        string
	InputS3URI     string
	OutputS3URI    string
	LabelAttribute string
	WorkteamARN    string
	Tags           []Tag
}

// LabelingJob describes a Ground Truth labeling job.
type LabelingJob struct {
	JobName          string
	JobARN           string
	RoleARN          string
	InputS3URI       string
	OutputS3URI      string
	LabelAttribute   string
	WorkteamARN      string
	Status           string
	LabeledObjects   int
	FailureReason    string
	CreationTime     string
	LabelingEndTime  string
	LastModifiedTime string
	Tags             []Tag
}

// CompilationJobConfig describes a Neo compilation job to create.
type CompilationJobConfig struct {
	JobName      string
	RoleARN      string
	InputS3URI   string
	OutputS3URI  string
	TargetDevice string
	Framework    string
	Tags         []Tag
}

// CompilationJob describes a Neo compilation job.
type CompilationJob struct {
	JobName            string
	JobARN             string
	RoleARN            string
	InputS3URI         string
	OutputS3URI        string
	TargetDevice       string
	Framework          string
	Status             string
	FailureReason      string
	CreationTime       string
	CompilationEndTime string
	LastModifiedTime   string
	Tags               []Tag
}

// jobsAPI covers every asynchronous SageMaker job resource. Create transitions
// the job synchronously to a terminal state (Completed) so Describe/List are
// deterministic, mirroring the existing Bedrock customization-job mock.
type jobsAPI interface {
	CreateTrainingJob(ctx context.Context, cfg TrainingJobConfig) (*TrainingJob, error)
	DescribeTrainingJob(ctx context.Context, name string) (*TrainingJob, error)
	ListTrainingJobs(ctx context.Context) ([]TrainingJob, error)
	StopTrainingJob(ctx context.Context, name string) error

	CreateProcessingJob(ctx context.Context, cfg ProcessingJobConfig) (*ProcessingJob, error)
	DescribeProcessingJob(ctx context.Context, name string) (*ProcessingJob, error)
	ListProcessingJobs(ctx context.Context) ([]ProcessingJob, error)
	StopProcessingJob(ctx context.Context, name string) error

	CreateTransformJob(ctx context.Context, cfg TransformJobConfig) (*TransformJob, error)
	DescribeTransformJob(ctx context.Context, name string) (*TransformJob, error)
	ListTransformJobs(ctx context.Context) ([]TransformJob, error)
	StopTransformJob(ctx context.Context, name string) error

	CreateHyperParameterTuningJob(ctx context.Context, cfg HyperParameterTuningJobConfig) (*HyperParameterTuningJob, error)
	DescribeHyperParameterTuningJob(ctx context.Context, name string) (*HyperParameterTuningJob, error)
	ListHyperParameterTuningJobs(ctx context.Context) ([]HyperParameterTuningJob, error)
	StopHyperParameterTuningJob(ctx context.Context, name string) error

	CreateAutoMLJobV2(ctx context.Context, cfg AutoMLJobConfig) (*AutoMLJob, error)
	DescribeAutoMLJobV2(ctx context.Context, name string) (*AutoMLJob, error)
	ListAutoMLJobs(ctx context.Context) ([]AutoMLJob, error)
	StopAutoMLJob(ctx context.Context, name string) error

	CreateLabelingJob(ctx context.Context, cfg LabelingJobConfig) (*LabelingJob, error)
	DescribeLabelingJob(ctx context.Context, name string) (*LabelingJob, error)
	ListLabelingJobs(ctx context.Context) ([]LabelingJob, error)
	StopLabelingJob(ctx context.Context, name string) error

	CreateCompilationJob(ctx context.Context, cfg CompilationJobConfig) (*CompilationJob, error)
	DescribeCompilationJob(ctx context.Context, name string) (*CompilationJob, error)
	ListCompilationJobs(ctx context.Context) ([]CompilationJob, error)
	StopCompilationJob(ctx context.Context, name string) error
}
