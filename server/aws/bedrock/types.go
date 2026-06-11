package bedrock

// JSON wire shapes for the AWS Bedrock restJson1 protocol. Field names use the
// exact camelCase keys the real aws-sdk-go-v2 bedrock / bedrockruntime clients
// emit and expect, so requests decode and responses deserialize unchanged.

// dataConfig is the {s3Uri} shape shared by trainingDataConfig and
// outputDataConfig.
type dataConfig struct {
	S3URI string `json:"s3Uri,omitempty"`
}

type modelLifecycleJSON struct {
	Status string `json:"status"`
}

type foundationModelJSON struct {
	ModelARN                   string              `json:"modelArn"`
	ModelID                    string              `json:"modelId"`
	ModelName                  string              `json:"modelName,omitempty"`
	ProviderName               string              `json:"providerName,omitempty"`
	InputModalities            []string            `json:"inputModalities,omitempty"`
	OutputModalities           []string            `json:"outputModalities,omitempty"`
	ResponseStreamingSupported bool                `json:"responseStreamingSupported"`
	CustomizationsSupported    []string            `json:"customizationsSupported,omitempty"`
	InferenceTypesSupported    []string            `json:"inferenceTypesSupported,omitempty"`
	ModelLifecycle             *modelLifecycleJSON `json:"modelLifecycle,omitempty"`
}

type listFoundationModelsResponse struct {
	ModelSummaries []foundationModelJSON `json:"modelSummaries"`
}

type getFoundationModelResponse struct {
	ModelDetails foundationModelJSON `json:"modelDetails"`
}

type createJobRequest struct {
	JobName             string            `json:"jobName"`
	CustomModelName     string            `json:"customModelName"`
	RoleARN             string            `json:"roleArn"`
	BaseModelIdentifier string            `json:"baseModelIdentifier"`
	CustomizationType   string            `json:"customizationType"`
	ClientRequestToken  string            `json:"clientRequestToken"`
	HyperParameters     map[string]string `json:"hyperParameters"`
	TrainingDataConfig  *dataConfig       `json:"trainingDataConfig"`
	OutputDataConfig    *dataConfig       `json:"outputDataConfig"`
}

type createJobResponse struct {
	JobARN string `json:"jobArn"`
}

type jobJSON struct {
	JobARN             string            `json:"jobArn"`
	JobName            string            `json:"jobName"`
	OutputModelName    string            `json:"outputModelName"`
	OutputModelARN     string            `json:"outputModelArn,omitempty"`
	RoleARN            string            `json:"roleArn"`
	BaseModelARN       string            `json:"baseModelArn"`
	Status             string            `json:"status"`
	CustomizationType  string            `json:"customizationType,omitempty"`
	ClientRequestToken string            `json:"clientRequestToken,omitempty"`
	HyperParameters    map[string]string `json:"hyperParameters,omitempty"`
	TrainingDataConfig *dataConfig       `json:"trainingDataConfig,omitempty"`
	OutputDataConfig   *dataConfig       `json:"outputDataConfig,omitempty"`
	CreationTime       string            `json:"creationTime,omitempty"`
	LastModifiedTime   string            `json:"lastModifiedTime,omitempty"`
	EndTime            string            `json:"endTime,omitempty"`
	FailureMessage     string            `json:"failureMessage,omitempty"`
}

type jobSummaryJSON struct {
	JobARN            string `json:"jobArn"`
	JobName           string `json:"jobName"`
	BaseModelARN      string `json:"baseModelArn"`
	CustomModelName   string `json:"customModelName,omitempty"`
	CustomModelARN    string `json:"customModelArn,omitempty"`
	Status            string `json:"status"`
	CustomizationType string `json:"customizationType,omitempty"`
	CreationTime      string `json:"creationTime,omitempty"`
	LastModifiedTime  string `json:"lastModifiedTime,omitempty"`
	EndTime           string `json:"endTime,omitempty"`
}

type listJobsResponse struct {
	ModelCustomizationJobSummaries []jobSummaryJSON `json:"modelCustomizationJobSummaries"`
	NextToken                      string           `json:"nextToken,omitempty"`
}

type customModelSummaryJSON struct {
	ModelARN          string `json:"modelArn"`
	ModelName         string `json:"modelName"`
	BaseModelARN      string `json:"baseModelArn,omitempty"`
	BaseModelName     string `json:"baseModelName,omitempty"`
	CustomizationType string `json:"customizationType,omitempty"`
	ModelStatus       string `json:"modelStatus,omitempty"`
	OwnerAccountID    string `json:"ownerAccountId,omitempty"`
	CreationTime      string `json:"creationTime,omitempty"`
}

type listCustomModelsResponse struct {
	ModelSummaries []customModelSummaryJSON `json:"modelSummaries"`
	NextToken      string                   `json:"nextToken,omitempty"`
}

type getCustomModelResponse struct {
	ModelARN           string            `json:"modelArn"`
	ModelName          string            `json:"modelName"`
	JobARN             string            `json:"jobArn,omitempty"`
	JobName            string            `json:"jobName,omitempty"`
	BaseModelARN       string            `json:"baseModelArn,omitempty"`
	CustomizationType  string            `json:"customizationType,omitempty"`
	ModelStatus        string            `json:"modelStatus,omitempty"`
	HyperParameters    map[string]string `json:"hyperParameters,omitempty"`
	TrainingDataConfig *dataConfig       `json:"trainingDataConfig,omitempty"`
	OutputDataConfig   *dataConfig       `json:"outputDataConfig,omitempty"`
	CreationTime       string            `json:"creationTime,omitempty"`
}

// Converse request shapes. Content blocks carry only the text member; other
// members (image, document, toolUse) decode to an empty Text and are ignored.

type converseTextBlock struct {
	Text string `json:"text"`
}

type converseMessage struct {
	Role    string              `json:"role"`
	Content []converseTextBlock `json:"content"`
}

type converseInferenceConfig struct {
	MaxTokens     *int32   `json:"maxTokens,omitempty"`
	Temperature   *float64 `json:"temperature,omitempty"`
	TopP          *float64 `json:"topP,omitempty"`
	StopSequences []string `json:"stopSequences,omitempty"`
}

type converseRequest struct {
	Messages        []converseMessage        `json:"messages"`
	System          []converseTextBlock      `json:"system"`
	InferenceConfig *converseInferenceConfig `json:"inferenceConfig"`
}

// Converse response shapes.

type converseUsage struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
	TotalTokens  int `json:"totalTokens"`
}

type converseMetrics struct {
	LatencyMs int `json:"latencyMs"`
}

type converseOutputMessage struct {
	Role    string              `json:"role"`
	Content []converseTextBlock `json:"content"`
}

type converseOutputUnion struct {
	Message converseOutputMessage `json:"message"`
}

type converseResponse struct {
	Output     converseOutputUnion `json:"output"`
	StopReason string              `json:"stopReason"`
	Usage      converseUsage       `json:"usage"`
	Metrics    converseMetrics     `json:"metrics"`
}
