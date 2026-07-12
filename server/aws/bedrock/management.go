package bedrock

import (
	"net/http"

	bedrockdriver "github.com/stackshy/cloudemu/v2/services/bedrock/driver"
)

// --- wire types ---

type tagPair struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type createGuardrailRequest struct {
	Name                    string    `json:"name"`
	Description             string    `json:"description"`
	BlockedInputMessaging   string    `json:"blockedInputMessaging"`
	BlockedOutputsMessaging string    `json:"blockedOutputsMessaging"`
	KMSKeyID                string    `json:"kmsKeyId"`
	ClientRequestToken      string    `json:"clientRequestToken"`
	Tags                    []tagPair `json:"tags"`
}

type createGuardrailResponse struct {
	GuardrailID  string `json:"guardrailId"`
	GuardrailARN string `json:"guardrailArn"`
	Version      string `json:"version"`
	CreatedAt    string `json:"createdAt,omitempty"`
}

type guardrailJSON struct {
	Name                    string `json:"name"`
	Description             string `json:"description,omitempty"`
	GuardrailID             string `json:"guardrailId"`
	GuardrailARN            string `json:"guardrailArn"`
	Version                 string `json:"version"`
	Status                  string `json:"status"`
	BlockedInputMessaging   string `json:"blockedInputMessaging,omitempty"`
	BlockedOutputsMessaging string `json:"blockedOutputsMessaging,omitempty"`
	KMSKeyARN               string `json:"kmsKeyArn,omitempty"`
	CreatedAt               string `json:"createdAt,omitempty"`
	UpdatedAt               string `json:"updatedAt,omitempty"`
}

type guardrailSummaryJSON struct {
	ID          string `json:"id"`
	ARN         string `json:"arn"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version"`
	Status      string `json:"status"`
	CreatedAt   string `json:"createdAt,omitempty"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
}

type listGuardrailsResponse struct {
	Guardrails []guardrailSummaryJSON `json:"guardrails"`
	NextToken  string                 `json:"nextToken,omitempty"`
}

type updateGuardrailResponse struct {
	GuardrailID  string `json:"guardrailId"`
	GuardrailARN string `json:"guardrailArn"`
	Version      string `json:"version"`
	UpdatedAt    string `json:"updatedAt,omitempty"`
}

type createProvisionedRequest struct {
	ProvisionedModelName string    `json:"provisionedModelName"`
	ModelID              string    `json:"modelId"`
	ModelUnits           int       `json:"modelUnits"`
	CommitmentDuration   string    `json:"commitmentDuration"`
	ClientRequestToken   string    `json:"clientRequestToken"`
	Tags                 []tagPair `json:"tags"`
}

type createProvisionedResponse struct {
	ProvisionedModelARN string `json:"provisionedModelArn"`
}

type provisionedJSON struct {
	ProvisionedModelName string `json:"provisionedModelName"`
	ProvisionedModelARN  string `json:"provisionedModelArn"`
	ModelARN             string `json:"modelArn"`
	DesiredModelARN      string `json:"desiredModelArn"`
	FoundationModelARN   string `json:"foundationModelArn"`
	ModelUnits           int    `json:"modelUnits"`
	DesiredModelUnits    int    `json:"desiredModelUnits"`
	Status               string `json:"status"`
	CommitmentDuration   string `json:"commitmentDuration,omitempty"`
	CreationTime         string `json:"creationTime,omitempty"`
	LastModifiedTime     string `json:"lastModifiedTime,omitempty"`
}

type listProvisionedResponse struct {
	ProvisionedModelSummaries []provisionedJSON `json:"provisionedModelSummaries"`
	NextToken                 string            `json:"nextToken,omitempty"`
}

type s3ConfigJSON struct {
	BucketName string `json:"bucketName"`
	KeyPrefix  string `json:"keyPrefix,omitempty"`
}

type cloudWatchConfigJSON struct {
	LogGroupName              string        `json:"logGroupName"`
	RoleArn                   string        `json:"roleArn"`
	LargeDataDeliveryS3Config *s3ConfigJSON `json:"largeDataDeliveryS3Config,omitempty"`
}

type loggingConfigJSON struct {
	TextDataDeliveryEnabled      bool                  `json:"textDataDeliveryEnabled"`
	ImageDataDeliveryEnabled     bool                  `json:"imageDataDeliveryEnabled"`
	EmbeddingDataDeliveryEnabled bool                  `json:"embeddingDataDeliveryEnabled"`
	VideoDataDeliveryEnabled     bool                  `json:"videoDataDeliveryEnabled"`
	S3Config                     *s3ConfigJSON         `json:"s3Config,omitempty"`
	CloudWatchConfig             *cloudWatchConfigJSON `json:"cloudWatchConfig,omitempty"`
}

type putLoggingRequest struct {
	LoggingConfig loggingConfigJSON `json:"loggingConfig"`
}

type getLoggingResponse struct {
	LoggingConfig *loggingConfigJSON `json:"loggingConfig,omitempty"`
}

// --- guardrail operations ---

func (h *Handler) createGuardrail(w http.ResponseWriter, r *http.Request) {
	var in createGuardrailRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	g, err := h.bedrock.CreateGuardrail(r.Context(), bedrockdriver.GuardrailConfig{
		Name:                    in.Name,
		Description:             in.Description,
		BlockedInputMessaging:   in.BlockedInputMessaging,
		BlockedOutputsMessaging: in.BlockedOutputsMessaging,
		KMSKeyID:                in.KMSKeyID,
		ClientRequestToken:      in.ClientRequestToken,
		Tags:                    tagsToMap(in.Tags),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, createGuardrailResponse{
		GuardrailID: g.ID, GuardrailARN: g.ARN, Version: g.Version, CreatedAt: g.CreatedAt,
	})
}

func (h *Handler) getGuardrail(w http.ResponseWriter, r *http.Request, id string) {
	g, err := h.bedrock.GetGuardrail(r.Context(), id, r.URL.Query().Get("guardrailVersion"))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, toGuardrailJSON(g))
}

func (h *Handler) listGuardrails(w http.ResponseWriter, r *http.Request) {
	gs, err := h.bedrock.ListGuardrails(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]guardrailSummaryJSON, 0, len(gs))
	for i := range gs {
		out = append(out, toGuardrailSummaryJSON(&gs[i]))
	}

	writeJSON(w, listGuardrailsResponse{Guardrails: out})
}

func (h *Handler) updateGuardrail(w http.ResponseWriter, r *http.Request, id string) {
	var in createGuardrailRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	g, err := h.bedrock.UpdateGuardrail(r.Context(), id, bedrockdriver.GuardrailConfig{
		Name:                    in.Name,
		Description:             in.Description,
		BlockedInputMessaging:   in.BlockedInputMessaging,
		BlockedOutputsMessaging: in.BlockedOutputsMessaging,
		KMSKeyID:                in.KMSKeyID,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, updateGuardrailResponse{
		GuardrailID: g.ID, GuardrailARN: g.ARN, Version: g.Version, UpdatedAt: g.UpdatedAt,
	})
}

func (h *Handler) deleteGuardrail(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.bedrock.DeleteGuardrail(r.Context(), id); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

// --- provisioned throughput operations ---

func (h *Handler) createProvisioned(w http.ResponseWriter, r *http.Request) {
	var in createProvisionedRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	pt, err := h.bedrock.CreateProvisionedModelThroughput(r.Context(), bedrockdriver.ProvisionedThroughputConfig{
		ProvisionedModelName: in.ProvisionedModelName,
		ModelID:              in.ModelID,
		ModelUnits:           in.ModelUnits,
		CommitmentDuration:   in.CommitmentDuration,
		ClientRequestToken:   in.ClientRequestToken,
		Tags:                 tagsToMap(in.Tags),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, createProvisionedResponse{ProvisionedModelARN: pt.ARN})
}

func (h *Handler) getProvisioned(w http.ResponseWriter, r *http.Request, id string) {
	pt, err := h.bedrock.GetProvisionedModelThroughput(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, toProvisionedJSON(pt))
}

func (h *Handler) listProvisioned(w http.ResponseWriter, r *http.Request) {
	pts, err := h.bedrock.ListProvisionedModelThroughputs(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]provisionedJSON, 0, len(pts))
	for i := range pts {
		out = append(out, toProvisionedJSON(&pts[i]))
	}

	writeJSON(w, listProvisionedResponse{ProvisionedModelSummaries: out})
}

func (h *Handler) deleteProvisioned(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.bedrock.DeleteProvisionedModelThroughput(r.Context(), id); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

// --- model invocation logging operations ---

func (h *Handler) putLogging(w http.ResponseWriter, r *http.Request) {
	var in putLoggingRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	if err := h.bedrock.PutModelInvocationLoggingConfiguration(r.Context(), toDriverLogging(in.LoggingConfig)); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) getLogging(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.bedrock.GetModelInvocationLoggingConfiguration(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	if cfg == nil {
		writeJSON(w, getLoggingResponse{})

		return
	}

	wire := toLoggingJSON(cfg)
	writeJSON(w, getLoggingResponse{LoggingConfig: &wire})
}

func (h *Handler) deleteLogging(w http.ResponseWriter, r *http.Request) {
	if err := h.bedrock.DeleteModelInvocationLoggingConfiguration(r.Context()); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

// --- converters ---

func toGuardrailJSON(g *bedrockdriver.Guardrail) guardrailJSON {
	return guardrailJSON{
		Name:                    g.Name,
		Description:             g.Description,
		GuardrailID:             g.ID,
		GuardrailARN:            g.ARN,
		Version:                 g.Version,
		Status:                  g.Status,
		BlockedInputMessaging:   g.BlockedInputMessaging,
		BlockedOutputsMessaging: g.BlockedOutputsMessaging,
		KMSKeyARN:               g.KMSKeyARN,
		CreatedAt:               g.CreatedAt,
		UpdatedAt:               g.UpdatedAt,
	}
}

func toGuardrailSummaryJSON(g *bedrockdriver.Guardrail) guardrailSummaryJSON {
	return guardrailSummaryJSON{
		ID: g.ID, ARN: g.ARN, Name: g.Name, Description: g.Description,
		Version: g.Version, Status: g.Status, CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt,
	}
}

func toProvisionedJSON(pt *bedrockdriver.ProvisionedThroughput) provisionedJSON {
	return provisionedJSON{
		ProvisionedModelName: pt.Name,
		ProvisionedModelARN:  pt.ARN,
		ModelARN:             pt.ModelARN,
		DesiredModelARN:      pt.DesiredModelARN,
		FoundationModelARN:   pt.FoundationModelARN,
		ModelUnits:           pt.ModelUnits,
		DesiredModelUnits:    pt.DesiredModelUnits,
		Status:               pt.Status,
		CommitmentDuration:   pt.CommitmentDuration,
		CreationTime:         pt.CreationTime,
		LastModifiedTime:     pt.LastModifiedTime,
	}
}

func toDriverLogging(in loggingConfigJSON) bedrockdriver.LoggingConfig {
	out := bedrockdriver.LoggingConfig{
		TextDataDeliveryEnabled:      in.TextDataDeliveryEnabled,
		ImageDataDeliveryEnabled:     in.ImageDataDeliveryEnabled,
		EmbeddingDataDeliveryEnabled: in.EmbeddingDataDeliveryEnabled,
		VideoDataDeliveryEnabled:     in.VideoDataDeliveryEnabled,
	}

	if in.S3Config != nil {
		out.S3 = &bedrockdriver.S3LoggingConfig{BucketName: in.S3Config.BucketName, KeyPrefix: in.S3Config.KeyPrefix}
	}

	if in.CloudWatchConfig != nil {
		cw := &bedrockdriver.CloudWatchLoggingConfig{
			LogGroupName: in.CloudWatchConfig.LogGroupName,
			RoleARN:      in.CloudWatchConfig.RoleArn,
		}
		if s3 := in.CloudWatchConfig.LargeDataDeliveryS3Config; s3 != nil {
			cw.LargeDataDeliveryS3 = &bedrockdriver.S3LoggingConfig{BucketName: s3.BucketName, KeyPrefix: s3.KeyPrefix}
		}

		out.CloudWatch = cw
	}

	return out
}

func toLoggingJSON(in *bedrockdriver.LoggingConfig) loggingConfigJSON {
	out := loggingConfigJSON{
		TextDataDeliveryEnabled:      in.TextDataDeliveryEnabled,
		ImageDataDeliveryEnabled:     in.ImageDataDeliveryEnabled,
		EmbeddingDataDeliveryEnabled: in.EmbeddingDataDeliveryEnabled,
		VideoDataDeliveryEnabled:     in.VideoDataDeliveryEnabled,
	}

	if in.S3 != nil {
		out.S3Config = &s3ConfigJSON{BucketName: in.S3.BucketName, KeyPrefix: in.S3.KeyPrefix}
	}

	if in.CloudWatch != nil {
		cw := &cloudWatchConfigJSON{LogGroupName: in.CloudWatch.LogGroupName, RoleArn: in.CloudWatch.RoleARN}
		if s3 := in.CloudWatch.LargeDataDeliveryS3; s3 != nil {
			cw.LargeDataDeliveryS3Config = &s3ConfigJSON{BucketName: s3.BucketName, KeyPrefix: s3.KeyPrefix}
		}

		out.CloudWatchConfig = cw
	}

	return out
}

func tagsToMap(tags []tagPair) map[string]string {
	if len(tags) == 0 {
		return nil
	}

	out := make(map[string]string, len(tags))
	for _, t := range tags {
		out[t.Key] = t.Value
	}

	return out
}
