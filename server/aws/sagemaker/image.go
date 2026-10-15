package sagemaker

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

func imageToWire(img *driver.Image) map[string]any {
	out := map[string]any{
		"ImageName": img.ImageName, "ImageArn": img.ImageARN, "ImageStatus": img.Status,
		"CreationTime": epoch(img.CreationTime), "LastModifiedTime": epoch(img.LastModifiedTime),
	}
	setIf(out, "DisplayName", img.DisplayName)
	setIf(out, "Description", img.Description)
	setIf(out, "FailureReason", img.FailureReason)

	return out
}

func (h *Handler) createImage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ImageName   string    `json:"ImageName"`
		RoleArn     string    `json:"RoleArn"`
		DisplayName string    `json:"DisplayName"`
		Description string    `json:"Description"`
		Tags        []wireTag `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	img, err := h.svc.CreateImage(r.Context(), driver.ImageSpec{
		Name: req.ImageName, RoleARN: req.RoleArn, DisplayName: req.DisplayName, Description: req.Description, Tags: toTags(req.Tags),
	})
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ImageArn": img.ImageARN})
}

func (h *Handler) describeImage(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "ImageName")
	if !ok {
		return
	}

	img, err := h.svc.DescribeImage(r.Context(), name)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	resp := imageToWire(img)
	resp["RoleArn"] = img.RoleARN

	wire.WriteJSON(w, resp)
}

func (h *Handler) listImages(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeListReq(w, r)
	if !ok {
		return
	}

	page, err := h.svc.ListImages(r.Context(), req.options())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "Images", page, imageToWire)
}

func (h *Handler) updateImage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ImageName        string   `json:"ImageName"`
		DisplayName      *string  `json:"DisplayName"`
		Description      *string  `json:"Description"`
		RoleArn          *string  `json:"RoleArn"`
		DeleteProperties []string `json:"DeleteProperties"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	img, err := h.svc.UpdateImage(r.Context(), driver.ImageUpdate{
		Name: req.ImageName, DisplayName: req.DisplayName, Description: req.Description, RoleARN: req.RoleArn,
		DeleteProperties: req.DeleteProperties,
	})
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ImageArn": img.ImageARN})
}

func (h *Handler) deleteImage(w http.ResponseWriter, r *http.Request) {
	stopByName(w, r, "ImageName", h.svc.DeleteImage)
}

// --- Image versions ---

func versionToWire(v *driver.ImageVersion) map[string]any {
	out := map[string]any{
		"ImageArn": v.ImageARN, "ImageVersionArn": v.ImageVersionARN, "Version": v.Version, "ImageVersionStatus": v.Status,
		"CreationTime": epoch(v.CreationTime), "LastModifiedTime": epoch(v.LastModifiedTime),
	}
	setIf(out, "FailureReason", v.FailureReason)

	return out
}

func (h *Handler) createImageVersion(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ImageName       string   `json:"ImageName"`
		BaseImage       string   `json:"BaseImage"`
		ClientToken     string   `json:"ClientToken"`
		Aliases         []string `json:"Aliases"`
		Horovod         bool     `json:"Horovod"`
		JobType         string   `json:"JobType"`
		MLFramework     string   `json:"MLFramework"`
		Processor       string   `json:"Processor"`
		ProgrammingLang string   `json:"ProgrammingLang"`
		ReleaseNotes    string   `json:"ReleaseNotes"`
		VendorGuidance  string   `json:"VendorGuidance"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	v, err := h.svc.CreateImageVersion(r.Context(), driver.ImageVersionSpec{
		ImageName: req.ImageName, BaseImage: req.BaseImage, ClientToken: req.ClientToken, Aliases: req.Aliases,
		Horovod: req.Horovod, JobType: req.JobType, MLFramework: req.MLFramework, Processor: req.Processor,
		ProgrammingLang: req.ProgrammingLang, ReleaseNotes: req.ReleaseNotes, VendorGuidance: req.VendorGuidance,
	})
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ImageVersionArn": v.ImageVersionARN})
}

// versionRef decodes the ImageName / Version / Alias addressing members.
type versionRef struct {
	ImageName string `json:"ImageName"`
	Version   *int   `json:"Version"`
	Alias     string `json:"Alias"`
}

func (v versionRef) ref() driver.ImageVersionRef {
	return driver.ImageVersionRef{ImageName: v.ImageName, Version: v.Version, Alias: v.Alias}
}

func (h *Handler) describeImageVersion(w http.ResponseWriter, r *http.Request) {
	var req versionRef
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	v, err := h.svc.DescribeImageVersion(r.Context(), req.ref())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	resp := versionToWire(v)
	resp["BaseImage"] = v.BaseImage
	resp["ContainerImage"] = v.ContainerImage

	if v.Horovod {
		resp["Horovod"] = true
	}

	for k, val := range map[string]string{
		"JobType": v.JobType, "MLFramework": v.MLFramework, "Processor": v.Processor,
		"ProgrammingLang": v.ProgrammingLang, "ReleaseNotes": v.ReleaseNotes, "VendorGuidance": v.VendorGuidance,
	} {
		setIf(resp, k, val)
	}

	wire.WriteJSON(w, resp)
}

func (h *Handler) listImageVersions(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeListReq(w, r)
	if !ok {
		return
	}

	page, err := h.svc.ListImageVersions(r.Context(), req.ImageName, req.options())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "ImageVersions", page, versionToWire)
}

func (h *Handler) updateImageVersion(w http.ResponseWriter, r *http.Request) {
	var req struct {
		versionRef
		AliasesToAdd    []string `json:"AliasesToAdd"`
		AliasesToDelete []string `json:"AliasesToDelete"`
		Horovod         *bool    `json:"Horovod"`
		JobType         *string  `json:"JobType"`
		MLFramework     *string  `json:"MLFramework"`
		Processor       *string  `json:"Processor"`
		ProgrammingLang *string  `json:"ProgrammingLang"`
		ReleaseNotes    *string  `json:"ReleaseNotes"`
		VendorGuidance  *string  `json:"VendorGuidance"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	v, err := h.svc.UpdateImageVersion(r.Context(), driver.ImageVersionUpdate{
		Ref: req.ref(), AliasesToAdd: req.AliasesToAdd, AliasesToDelete: req.AliasesToDelete, Horovod: req.Horovod,
		JobType: req.JobType, MLFramework: req.MLFramework, Processor: req.Processor, ProgrammingLang: req.ProgrammingLang,
		ReleaseNotes: req.ReleaseNotes, VendorGuidance: req.VendorGuidance,
	})
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ImageVersionArn": v.ImageVersionARN})
}

func (h *Handler) deleteImageVersion(w http.ResponseWriter, r *http.Request) {
	var req versionRef
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.svc.DeleteImageVersion(r.Context(), req.ref()); err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{})
}

func (h *Handler) listAliases(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeListReq(w, r)
	if !ok {
		return
	}

	page, err := h.svc.ListAliases(r.Context(), driver.ImageVersionRef{ImageName: req.ImageName, Version: req.Version, Alias: req.Alias}, req.options()) //nolint:lll // SDK-shaped literal
	if err != nil {
		writeDriverError(w, err)

		return
	}

	resp := map[string]any{"SageMakerImageVersionAliases": page.Items}
	if page.NextToken != "" {
		resp["NextToken"] = page.NextToken
	}

	wire.WriteJSON(w, resp)
}

// --- App image configs ---

type wireFileSystem struct {
	DefaultGID *int   `json:"DefaultGid,omitempty"`
	DefaultUID *int   `json:"DefaultUid,omitempty"`
	MountPath  string `json:"MountPath"`
}

type wireContainerConfig struct {
	ContainerArguments   []string          `json:"ContainerArguments,omitempty"`
	ContainerEntrypoint  []string          `json:"ContainerEntrypoint,omitempty"`
	ContainerEnvironment map[string]string `json:"ContainerEnvironmentVariables,omitempty"`
}

type wireKernelSpec struct {
	Name        string `json:"Name"`
	DisplayName string `json:"DisplayName,omitempty"`
}

type wireAppBody struct {
	KernelSpecs      []wireKernelSpec     `json:"KernelSpecs,omitempty"`
	ContainerConfig  *wireContainerConfig `json:"ContainerConfig,omitempty"`
	FileSystemConfig *wireFileSystem      `json:"FileSystemConfig,omitempty"`
}

func toAppBody(b *wireAppBody) *driver.AppImageConfigBody {
	if b == nil {
		return nil
	}

	out := &driver.AppImageConfigBody{}
	for _, k := range b.KernelSpecs {
		out.KernelSpecs = append(out.KernelSpecs, driver.KernelSpec{Name: k.Name, DisplayName: k.DisplayName})
	}

	if c := b.ContainerConfig; c != nil {
		out.ContainerConfig = &driver.ContainerConfig{Arguments: c.ContainerArguments, Entrypoint: c.ContainerEntrypoint, Env: c.ContainerEnvironment} //nolint:lll // SDK-shaped literal
	}

	if f := b.FileSystemConfig; f != nil {
		out.FileSystemConfig = &driver.FileSystemConfig{MountPath: f.MountPath, DefaultUID: f.DefaultUID, DefaultGID: f.DefaultGID}
	}

	return out
}

func fromAppBody(b *driver.AppImageConfigBody) *wireAppBody {
	if b == nil {
		return nil
	}

	out := &wireAppBody{}
	for _, k := range b.KernelSpecs {
		out.KernelSpecs = append(out.KernelSpecs, wireKernelSpec{Name: k.Name, DisplayName: k.DisplayName})
	}

	if c := b.ContainerConfig; c != nil {
		out.ContainerConfig = &wireContainerConfig{ContainerArguments: c.Arguments, ContainerEntrypoint: c.Entrypoint, ContainerEnvironment: c.Env} //nolint:lll // SDK-shaped literal
	}

	if f := b.FileSystemConfig; f != nil {
		out.FileSystemConfig = &wireFileSystem{MountPath: f.MountPath, DefaultUID: f.DefaultUID, DefaultGID: f.DefaultGID}
	}

	return out
}

func appConfigToWire(c *driver.AppImageConfig) map[string]any {
	out := map[string]any{
		"AppImageConfigArn": c.ARN, "AppImageConfigName": c.Name,
		"CreationTime": epoch(c.CreationTime), "LastModifiedTime": epoch(c.LastModifiedTime),
	}

	if c.KernelGateway != nil {
		out["KernelGatewayImageConfig"] = fromAppBody(c.KernelGateway)
	}

	if c.JupyterLab != nil {
		out["JupyterLabAppImageConfig"] = fromAppBody(c.JupyterLab)
	}

	if c.CodeEditor != nil {
		out["CodeEditorAppImageConfig"] = fromAppBody(c.CodeEditor)
	}

	return out
}

type wireAppConfigRequest struct {
	AppImageConfigName       string       `json:"AppImageConfigName"`
	KernelGatewayImageConfig *wireAppBody `json:"KernelGatewayImageConfig"`
	JupyterLabAppImageConfig *wireAppBody `json:"JupyterLabAppImageConfig"`
	CodeEditorAppImageConfig *wireAppBody `json:"CodeEditorAppImageConfig"`
	Tags                     []wireTag    `json:"Tags"`
}

func (q wireAppConfigRequest) spec() driver.AppImageConfigSpec {
	return driver.AppImageConfigSpec{
		Name: q.AppImageConfigName, KernelGateway: toAppBody(q.KernelGatewayImageConfig),
		JupyterLab: toAppBody(q.JupyterLabAppImageConfig), CodeEditor: toAppBody(q.CodeEditorAppImageConfig), Tags: toTags(q.Tags),
	}
}

func (h *Handler) createAppImageConfig(w http.ResponseWriter, r *http.Request) {
	var req wireAppConfigRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	c, err := h.svc.CreateAppImageConfig(r.Context(), req.spec())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"AppImageConfigArn": c.ARN})
}

func (h *Handler) describeAppImageConfig(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "AppImageConfigName")
	if !ok {
		return
	}

	c, err := h.svc.DescribeAppImageConfig(r.Context(), name)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, appConfigToWire(c))
}

func (h *Handler) listAppImageConfigs(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeListReq(w, r)
	if !ok {
		return
	}

	page, err := h.svc.ListAppImageConfigs(r.Context(), req.options())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "AppImageConfigs", page, appConfigToWire)
}

func (h *Handler) updateAppImageConfig(w http.ResponseWriter, r *http.Request) {
	var req wireAppConfigRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	c, err := h.svc.UpdateAppImageConfig(r.Context(), req.spec())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"AppImageConfigArn": c.ARN})
}

func (h *Handler) deleteAppImageConfig(w http.ResponseWriter, r *http.Request) {
	stopByName(w, r, "AppImageConfigName", h.svc.DeleteAppImageConfig)
}
