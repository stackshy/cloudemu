package driver

// Async invocation status values (bedrock-runtime). The set is deliberately
// smaller than the customization-job set: there is no "Submitted".
const (
	AsyncInProgress = "InProgress"
	AsyncCompleted  = "Completed"
	AsyncFailed     = "Failed"
)

// Evaluation-job type values.
const (
	EvaluationTypeAutomated = "Automated"
	EvaluationTypeHuman     = "Human"
)

// AsyncInvokeOutputConfig is the S3 delivery target for an async invocation's
// output, mirroring the s3OutputDataConfig union member.
type AsyncInvokeOutputConfig struct {
	S3URI       string
	BucketOwner string
	KMSKeyID    string
}

// StartAsyncInvokeConfig describes an asynchronous model invocation to start.
// ModelInput is a model-native JSON document carried through verbatim.
type StartAsyncInvokeConfig struct {
	ClientRequestToken string
	ModelID            string
	ModelInput         []byte
	Output             AsyncInvokeOutputConfig
	Tags               map[string]string
}

// AsyncInvoke describes an asynchronous model invocation.
type AsyncInvoke struct {
	InvocationARN      string
	ModelARN           string
	ClientRequestToken string
	Status             string
	Output             AsyncInvokeOutputConfig
	FailureMessage     string
	SubmitTime         string
	LastModifiedTime   string
	EndTime            string
}

// ModelImportJobConfig describes a custom-model import job to create.
type ModelImportJobConfig struct {
	JobName               string
	ImportedModelName     string
	RoleARN               string
	ModelDataSourceS3URI  string
	ClientRequestToken    string
	ImportedModelKMSKeyID string
	JobTags               map[string]string
	ImportedModelTags     map[string]string
}

// ModelImportJob describes a custom-model import job.
type ModelImportJob struct {
	JobARN               string
	JobName              string
	ImportedModelName    string
	ImportedModelARN     string
	RoleARN              string
	ModelDataSourceS3URI string
	Status               string
	FailureMessage       string
	CreationTime         string
	LastModifiedTime     string
	EndTime              string
}

// ModelCopyJobConfig describes a model-copy job to create.
type ModelCopyJobConfig struct {
	SourceModelARN     string
	TargetModelName    string
	ClientRequestToken string
	ModelKMSKeyID      string
	TargetModelTags    map[string]string
}

// ModelCopyJob describes a model-copy job.
type ModelCopyJob struct {
	JobARN               string
	SourceAccountID      string
	SourceModelARN       string
	SourceModelName      string
	TargetModelName      string
	TargetModelARN       string
	TargetModelKMSKeyARN string
	Status               string
	FailureMessage       string
	CreationTime         string
}

// EvaluationJobConfig describes an evaluation job to create. EvaluationConfig
// and InferenceConfig are opaque JSON documents carried through verbatim.
type EvaluationJobConfig struct {
	JobName                 string
	RoleARN                 string
	EvaluationConfig        []byte
	InferenceConfig         []byte
	OutputDataS3URI         string
	ApplicationType         string
	ClientRequestToken      string
	CustomerEncryptionKeyID string
	JobDescription          string
	JobTags                 map[string]string
}

// EvaluationJob describes a model-evaluation job.
type EvaluationJob struct {
	JobARN                  string
	JobName                 string
	JobType                 string
	ApplicationType         string
	RoleARN                 string
	EvaluationConfig        []byte
	InferenceConfig         []byte
	OutputDataS3URI         string
	JobDescription          string
	CustomerEncryptionKeyID string
	Status                  string
	FailureMessages         []string
	CreationTime            string
	LastModifiedTime        string
}
