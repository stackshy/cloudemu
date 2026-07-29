package bedrockagentruntime

// JSON wire shapes for the AWS Bedrock Agent runtime restJson1 protocol. Field
// names use the exact camelCase keys the real aws-sdk-go-v2
// bedrockagentruntime client emits and expects, so requests decode and
// responses deserialize unchanged.

// --- InvokeAgent request (path params carry agent/alias/session ids) ---

type invokeAgentRequest struct {
	InputText   string `json:"inputText"`
	EnableTrace bool   `json:"enableTrace"`
	EndSession  bool   `json:"endSession"`
}

// --- Retrieve ---

type retrievalQuery struct {
	Text string `json:"text"`
}

type retrieveRequest struct {
	RetrievalQuery retrievalQuery `json:"retrievalQuery"`
	NextToken      string         `json:"nextToken"`
}

type retrievalResultContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type retrievalResultS3Location struct {
	URI string `json:"uri"`
}

type retrievalResultLocation struct {
	Type       string                     `json:"type"`
	S3Location *retrievalResultS3Location `json:"s3Location,omitempty"`
}

type knowledgeBaseRetrievalResult struct {
	Content  retrievalResultContent   `json:"content"`
	Location *retrievalResultLocation `json:"location,omitempty"`
	Score    float64                  `json:"score"`
}

type retrieveResponse struct {
	RetrievalResults []knowledgeBaseRetrievalResult `json:"retrievalResults"`
	NextToken        string                         `json:"nextToken,omitempty"`
}

// --- RetrieveAndGenerate ---

type retrieveAndGenerateInputBody struct {
	Text string `json:"text"`
}

type retrieveAndGenerateRequest struct {
	Input     retrieveAndGenerateInputBody `json:"input"`
	SessionID string                       `json:"sessionId"`
}

type retrieveAndGenerateOutputBody struct {
	Text string `json:"text"`
}

type retrieveAndGenerateResponse struct {
	Output    retrieveAndGenerateOutputBody `json:"output"`
	SessionID string                        `json:"sessionId"`
	Citations []any                         `json:"citations"`
}
