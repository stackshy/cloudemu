package ssm

import (
	"net/http"
	"strconv"
	"time"

	ssmnative "github.com/stackshy/cloudemu/v2/providers/aws/ssm/driver"
	"github.com/stackshy/cloudemu/v2/server/wire"
)

// Run Command page sizes.
const (
	maxResultsCommands = 50 // ListCommands / ListCommandInvocations: 1..50
	maxResultsNodes    = 50 // DescribeInstanceInformation: 5..50
	defaultNodesPage   = 10
	minNodesPage       = 5
)

func runCommandOps() map[string]handlerFunc {
	return map[string]handlerFunc{
		"SendCommand":                 (*Handler).sendCommand,
		"GetCommandInvocation":        (*Handler).getCommandInvocation,
		"ListCommands":                (*Handler).listCommands,
		"ListCommandInvocations":      (*Handler).listCommandInvocations,
		"CancelCommand":               (*Handler).cancelCommand,
		"DescribeInstanceInformation": (*Handler).describeInstanceInformation,
	}
}

type ssmTarget struct {
	Key    string   `json:"Key"`
	Values []string `json:"Values"`
}

type notificationConfigJSON struct {
	NotificationArn    string   `json:"NotificationArn,omitempty"`
	NotificationEvents []string `json:"NotificationEvents,omitempty"`
	NotificationType   string   `json:"NotificationType,omitempty"`
}

type cloudWatchOutputJSON struct {
	CloudWatchLogGroupName  string `json:"CloudWatchLogGroupName,omitempty"`
	CloudWatchOutputEnabled bool   `json:"CloudWatchOutputEnabled"`
}

type sendCommandRequest struct {
	InstanceIDs            []string                `json:"InstanceIds"`
	Targets                []ssmTarget             `json:"Targets"`
	DocumentName           string                  `json:"DocumentName"`
	DocumentVersion        string                  `json:"DocumentVersion"`
	Comment                string                  `json:"Comment"`
	Parameters             map[string][]string     `json:"Parameters"`
	TimeoutSeconds         int32                   `json:"TimeoutSeconds"`
	MaxConcurrency         string                  `json:"MaxConcurrency"`
	MaxErrors              string                  `json:"MaxErrors"`
	OutputS3Region         string                  `json:"OutputS3Region"`
	OutputS3BucketName     string                  `json:"OutputS3BucketName"`
	OutputS3KeyPrefix      string                  `json:"OutputS3KeyPrefix"`
	ServiceRoleArn         string                  `json:"ServiceRoleArn"`
	NotificationConfig     *notificationConfigJSON `json:"NotificationConfig"`
	CloudWatchOutputConfig *cloudWatchOutputJSON   `json:"CloudWatchOutputConfig"`
}

type commandJSON struct {
	CommandID              string                 `json:"CommandId"`
	DocumentName           string                 `json:"DocumentName"`
	DocumentVersion        string                 `json:"DocumentVersion"`
	Comment                string                 `json:"Comment"`
	ExpiresAfter           float64                `json:"ExpiresAfter"`
	Parameters             map[string][]string    `json:"Parameters"`
	InstanceIDs            []string               `json:"InstanceIds"`
	Targets                []ssmTarget            `json:"Targets"`
	RequestedDateTime      float64                `json:"RequestedDateTime"`
	Status                 string                 `json:"Status"`
	StatusDetails          string                 `json:"StatusDetails"`
	OutputS3Region         string                 `json:"OutputS3Region,omitempty"`
	OutputS3BucketName     string                 `json:"OutputS3BucketName"`
	OutputS3KeyPrefix      string                 `json:"OutputS3KeyPrefix"`
	MaxConcurrency         string                 `json:"MaxConcurrency"`
	MaxErrors              string                 `json:"MaxErrors"`
	TargetCount            int32                  `json:"TargetCount"`
	CompletedCount         int32                  `json:"CompletedCount"`
	ErrorCount             int32                  `json:"ErrorCount"`
	DeliveryTimedOutCount  int32                  `json:"DeliveryTimedOutCount"`
	ServiceRole            string                 `json:"ServiceRole"`
	NotificationConfig     notificationConfigJSON `json:"NotificationConfig"`
	CloudWatchOutputConfig cloudWatchOutputJSON   `json:"CloudWatchOutputConfig"`
	TimeoutSeconds         int32                  `json:"TimeoutSeconds"`
}

type commandPluginJSON struct {
	Name                   string   `json:"Name"`
	Status                 string   `json:"Status"`
	StatusDetails          string   `json:"StatusDetails"`
	ResponseCode           int32    `json:"ResponseCode"`
	ResponseStartDateTime  *float64 `json:"ResponseStartDateTime,omitempty"`
	ResponseFinishDateTime *float64 `json:"ResponseFinishDateTime,omitempty"`
	Output                 string   `json:"Output"`
	StandardOutputURL      string   `json:"StandardOutputUrl"`
	StandardErrorURL       string   `json:"StandardErrorUrl"`
	OutputS3Region         string   `json:"OutputS3Region,omitempty"`
	OutputS3BucketName     string   `json:"OutputS3BucketName"`
	OutputS3KeyPrefix      string   `json:"OutputS3KeyPrefix"`
}

type commandInvocationJSON struct {
	CommandID              string                 `json:"CommandId"`
	InstanceID             string                 `json:"InstanceId"`
	InstanceName           string                 `json:"InstanceName"`
	Comment                string                 `json:"Comment"`
	DocumentName           string                 `json:"DocumentName"`
	DocumentVersion        string                 `json:"DocumentVersion"`
	RequestedDateTime      float64                `json:"RequestedDateTime"`
	Status                 string                 `json:"Status"`
	StatusDetails          string                 `json:"StatusDetails"`
	TraceOutput            string                 `json:"TraceOutput"`
	StandardOutputURL      string                 `json:"StandardOutputUrl"`
	StandardErrorURL       string                 `json:"StandardErrorUrl"`
	CommandPlugins         []commandPluginJSON    `json:"CommandPlugins"`
	ServiceRole            string                 `json:"ServiceRole"`
	NotificationConfig     notificationConfigJSON `json:"NotificationConfig"`
	CloudWatchOutputConfig cloudWatchOutputJSON   `json:"CloudWatchOutputConfig"`
}

type getCommandInvocationRequest struct {
	CommandID  string `json:"CommandId"`
	InstanceID string `json:"InstanceId"`
	PluginName string `json:"PluginName"`
}

type getCommandInvocationResponse struct {
	CommandID              string               `json:"CommandId"`
	InstanceID             string               `json:"InstanceId"`
	Comment                string               `json:"Comment"`
	DocumentName           string               `json:"DocumentName"`
	DocumentVersion        string               `json:"DocumentVersion"`
	PluginName             string               `json:"PluginName"`
	ResponseCode           int32                `json:"ResponseCode"`
	ExecutionStartDateTime string               `json:"ExecutionStartDateTime"`
	ExecutionElapsedTime   string               `json:"ExecutionElapsedTime"`
	ExecutionEndDateTime   string               `json:"ExecutionEndDateTime"`
	Status                 string               `json:"Status"`
	StatusDetails          string               `json:"StatusDetails"`
	StandardOutputContent  string               `json:"StandardOutputContent"`
	StandardOutputURL      string               `json:"StandardOutputUrl"`
	StandardErrorContent   string               `json:"StandardErrorContent"`
	StandardErrorURL       string               `json:"StandardErrorUrl"`
	CloudWatchOutputConfig cloudWatchOutputJSON `json:"CloudWatchOutputConfig"`
}

type commandFilterJSON struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type listCommandsRequest struct {
	CommandID  string              `json:"CommandId"`
	InstanceID string              `json:"InstanceId"`
	MaxResults int32               `json:"MaxResults"`
	NextToken  string              `json:"NextToken"`
	Filters    []commandFilterJSON `json:"Filters"`
	Details    bool                `json:"Details"`
}

func (q *listCommandsRequest) query() ssmnative.CommandQuery {
	out := ssmnative.CommandQuery{CommandID: q.CommandID, InstanceID: q.InstanceID}
	for _, f := range q.Filters {
		out.Filters = append(out.Filters, ssmnative.CommandFilter{Key: f.Key, Value: f.Value})
	}

	return out
}

// runCommand reports whether the configured driver supports Run Command,
// writing the error response when it does not.
func (h *Handler) runCommand(w http.ResponseWriter) (ssmnative.RunCommand, bool) {
	rc, ok := h.store.(ssmnative.RunCommand)
	if !ok {
		wire.WriteJSONError(w, http.StatusBadRequest,
			"UnsupportedOperationException", "this driver does not support Run Command")
	}

	return rc, ok
}

func (h *Handler) sendCommand(w http.ResponseWriter, r *http.Request) {
	store, ok := h.runCommand(w)
	if !ok {
		return
	}

	var req sendCommandRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	cfg := ssmnative.CommandConfig{
		InstanceIDs: req.InstanceIDs, Targets: toDriverTargets(req.Targets), DocumentName: req.DocumentName,
		DocumentVersion: req.DocumentVersion, Comment: req.Comment, Parameters: req.Parameters,
		TimeoutSeconds: req.TimeoutSeconds, MaxConcurrency: req.MaxConcurrency, MaxErrors: req.MaxErrors,
		OutputS3Region: req.OutputS3Region, OutputS3BucketName: req.OutputS3BucketName,
		OutputS3KeyPrefix: req.OutputS3KeyPrefix, ServiceRoleArn: req.ServiceRoleArn,
	}

	if n := req.NotificationConfig; n != nil {
		cfg.Notification = &ssmnative.NotificationConfig{
			NotificationArn: n.NotificationArn, NotificationEvents: n.NotificationEvents, NotificationType: n.NotificationType,
		}
	}

	if c := req.CloudWatchOutputConfig; c != nil {
		cfg.CloudWatchOutput = &ssmnative.CloudWatchOutputConfig{
			LogGroupName: c.CloudWatchLogGroupName, OutputEnabled: c.CloudWatchOutputEnabled,
		}
	}

	cmd, err := store.SendCommand(r.Context(), cfg)
	if err != nil {
		// The provider names the exception: InvalidInstanceId, InvalidDocument,
		// InvalidDocumentVersion, InvalidParameters or ValidationException.
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"Command": toCommandJSON(cmd)})
}

func toCommandJSON(c *ssmnative.Command) commandJSON {
	out := commandJSON{
		CommandID: c.CommandID, DocumentName: c.DocumentName, DocumentVersion: c.DocumentVersion, Comment: c.Comment,
		ExpiresAfter: epoch(c.ExpiresAfter), Parameters: c.Parameters, InstanceIDs: nonNil(c.InstanceIDs),
		Targets: toWireTargets(c.Targets), RequestedDateTime: epoch(c.RequestedDateTime), Status: c.Status,
		StatusDetails: c.StatusDetails, OutputS3Region: c.OutputS3Region, OutputS3BucketName: c.OutputS3BucketName,
		OutputS3KeyPrefix: c.OutputS3KeyPrefix, MaxConcurrency: c.MaxConcurrency, MaxErrors: c.MaxErrors,
		TargetCount: c.TargetCount, CompletedCount: c.CompletedCount, ErrorCount: c.ErrorCount,
		DeliveryTimedOutCount: c.DeliveryTimedOutCount, ServiceRole: c.ServiceRole,
		NotificationConfig: toNotificationJSON(c.Notification), CloudWatchOutputConfig: toCloudWatchJSON(c.CloudWatchOutput),
		TimeoutSeconds: c.TimeoutSeconds,
	}

	if out.Parameters == nil {
		out.Parameters = map[string][]string{}
	}

	return out
}

func toNotificationJSON(n *ssmnative.NotificationConfig) notificationConfigJSON {
	if n == nil {
		return notificationConfigJSON{NotificationArn: "", NotificationEvents: []string{}}
	}

	return notificationConfigJSON{
		NotificationArn: n.NotificationArn, NotificationEvents: n.NotificationEvents, NotificationType: n.NotificationType,
	}
}

func toCloudWatchJSON(c *ssmnative.CloudWatchOutputConfig) cloudWatchOutputJSON {
	if c == nil {
		return cloudWatchOutputJSON{}
	}

	return cloudWatchOutputJSON{CloudWatchLogGroupName: c.LogGroupName, CloudWatchOutputEnabled: c.OutputEnabled}
}

// toDriverTargets converts wire Targets to the driver's CommandTarget shape.
func toDriverTargets(in []ssmTarget) []ssmnative.CommandTarget {
	if len(in) == 0 {
		return nil
	}

	out := make([]ssmnative.CommandTarget, 0, len(in))
	for _, t := range in {
		out = append(out, ssmnative.CommandTarget{Key: t.Key, Values: t.Values})
	}

	return out
}

func toWireTargets(in []ssmnative.CommandTarget) []ssmTarget {
	out := make([]ssmTarget, 0, len(in))
	for _, t := range in {
		out = append(out, ssmTarget{Key: t.Key, Values: t.Values})
	}

	return out
}

// isoTime renders the ExecutionStart/EndDateTime strings; "" before the
// plugin starts.
func isoTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

func optionalEpoch(t time.Time) *float64 {
	if t.IsZero() {
		return nil
	}

	e := epoch(t)

	return &e
}

func (h *Handler) getCommandInvocation(w http.ResponseWriter, r *http.Request) {
	store, ok := h.runCommand(w)
	if !ok {
		return
	}

	var req getCommandInvocationRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	inv, err := store.GetCommandInvocation(r.Context(), req.CommandID, req.InstanceID, req.PluginName)
	if err != nil {
		// InvocationDoesNotExist for an unknown pair, which callers branch on
		// while polling a command that has not registered yet.
		writeErr(w, err)

		return
	}

	elapsed := ""
	if !inv.ExecutionStartTime.IsZero() && !inv.ExecutionEndTime.IsZero() {
		elapsed = "PT" + strconv.FormatFloat(inv.ExecutionEndTime.Sub(inv.ExecutionStartTime).Seconds(), 'f', 3, 64) + "S"
	}

	wire.WriteJSON(w, getCommandInvocationResponse{
		CommandID: inv.CommandID, InstanceID: inv.InstanceID, Comment: inv.Comment, DocumentName: inv.DocumentName,
		DocumentVersion: inv.DocumentVersion, PluginName: inv.PluginName, ResponseCode: inv.ResponseCode,
		ExecutionStartDateTime: isoTime(inv.ExecutionStartTime), ExecutionElapsedTime: elapsed,
		ExecutionEndDateTime: isoTime(inv.ExecutionEndTime), Status: inv.Status, StatusDetails: inv.StatusDetails,
		StandardOutputContent: inv.Stdout, StandardOutputURL: inv.StandardOutputURL,
		StandardErrorContent: inv.Stderr, StandardErrorURL: inv.StandardErrorURL,
		CloudWatchOutputConfig: toCloudWatchJSON(inv.CloudWatchOutput),
	})
}

// commandPage slices a result set by NextToken and MaxResults, answering
// InvalidNextToken for a token this server did not issue.
func commandPage(w http.ResponseWriter, token string, maxResults int32, limit, total int) (start, end int, next string, ok bool) {
	if _, err := decodePageToken(token); err != nil {
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidNextToken", "The specified token isn't valid.")

		return 0, 0, "", false
	}

	start, end, next, err := pageWindow(token, maxResults, limit, total)
	if err != nil {
		writeErr(w, err)

		return 0, 0, "", false
	}

	return start, end, next, true
}

func (h *Handler) listCommands(w http.ResponseWriter, r *http.Request) {
	store, ok := h.runCommand(w)
	if !ok {
		return
	}

	var req listCommandsRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	cmds, err := store.ListCommands(r.Context(), req.query())
	if err != nil {
		writeErr(w, err)

		return
	}

	start, end, next, ok := commandPage(w, req.NextToken, req.MaxResults, maxResultsCommands, len(cmds))
	if !ok {
		return
	}

	out := make([]commandJSON, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, toCommandJSON(&cmds[i]))
	}

	wire.WriteJSON(w, map[string]any{"Commands": out, "NextToken": omitEmpty(next)})
}

func (h *Handler) listCommandInvocations(w http.ResponseWriter, r *http.Request) {
	store, ok := h.runCommand(w)
	if !ok {
		return
	}

	var req listCommandsRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	invs, err := store.ListCommandInvocations(r.Context(), req.query(), req.Details)
	if err != nil {
		writeErr(w, err)

		return
	}

	start, end, next, ok := commandPage(w, req.NextToken, req.MaxResults, maxResultsCommands, len(invs))
	if !ok {
		return
	}

	out := make([]commandInvocationJSON, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, toInvocationJSON(&invs[i]))
	}

	wire.WriteJSON(w, map[string]any{"CommandInvocations": out, "NextToken": omitEmpty(next)})
}

func toInvocationJSON(inv *ssmnative.CommandInvocation) commandInvocationJSON {
	out := commandInvocationJSON{
		CommandID: inv.CommandID, InstanceID: inv.InstanceID, InstanceName: inv.InstanceName, Comment: inv.Comment,
		DocumentName: inv.DocumentName, DocumentVersion: inv.DocumentVersion, RequestedDateTime: epoch(inv.RequestedDateTime),
		Status: inv.Status, StatusDetails: inv.StatusDetails, StandardOutputURL: inv.StandardOutputURL,
		StandardErrorURL: inv.StandardErrorURL, CommandPlugins: make([]commandPluginJSON, 0, len(inv.Plugins)),
		ServiceRole: inv.ServiceRole, NotificationConfig: toNotificationJSON(inv.Notification),
		CloudWatchOutputConfig: toCloudWatchJSON(inv.CloudWatchOutput),
	}

	for i := range inv.Plugins {
		p := &inv.Plugins[i]
		out.CommandPlugins = append(out.CommandPlugins, commandPluginJSON{
			Name: p.Name, Status: p.Status, StatusDetails: p.StatusDetails, ResponseCode: p.ResponseCode,
			ResponseStartDateTime: optionalEpoch(p.ResponseStartDateTime), ResponseFinishDateTime: optionalEpoch(p.ResponseFinishDateTime),
			Output: p.Output, StandardOutputURL: p.StandardOutputURL, StandardErrorURL: p.StandardErrorURL,
			OutputS3Region: p.OutputS3Region, OutputS3BucketName: p.OutputS3BucketName, OutputS3KeyPrefix: p.OutputS3KeyPrefix,
		})
	}

	return out
}

func (h *Handler) cancelCommand(w http.ResponseWriter, r *http.Request) {
	store, ok := h.runCommand(w)
	if !ok {
		return
	}

	var req struct {
		CommandID   string   `json:"CommandId"`
		InstanceIDs []string `json:"InstanceIds"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := store.CancelCommand(r.Context(), req.CommandID, req.InstanceIDs); err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{})
}

type instanceInfoFilterJSON struct {
	Key      string   `json:"key"`
	ValueSet []string `json:"valueSet"`
}

type instanceStringFilterJSON struct {
	Key    string   `json:"Key"`
	Values []string `json:"Values"`
}

type instanceInformationJSON struct {
	InstanceID       string  `json:"InstanceId"`
	PingStatus       string  `json:"PingStatus"`
	LastPingDateTime float64 `json:"LastPingDateTime"`
	AgentVersion     string  `json:"AgentVersion"`
	IsLatestVersion  bool    `json:"IsLatestVersion"`
	PlatformType     string  `json:"PlatformType"`
	PlatformName     string  `json:"PlatformName"`
	PlatformVersion  string  `json:"PlatformVersion"`
	ResourceType     string  `json:"ResourceType"`
	IPAddress        string  `json:"IPAddress,omitempty"`
	ComputerName     string  `json:"ComputerName,omitempty"`
	SourceID         string  `json:"SourceId"`
	SourceType       string  `json:"SourceType"`
}

func (h *Handler) describeInstanceInformation(w http.ResponseWriter, r *http.Request) {
	nodes, ok := h.store.(ssmnative.ManagedNodes)
	if !ok {
		wire.WriteJSONError(w, http.StatusBadRequest,
			"UnsupportedOperationException", "this driver does not support managed nodes")

		return
	}

	var req struct {
		InstanceInformationFilterList []instanceInfoFilterJSON   `json:"InstanceInformationFilterList"`
		Filters                       []instanceStringFilterJSON `json:"Filters"`
		MaxResults                    int32                      `json:"MaxResults"`
		NextToken                     string                     `json:"NextToken"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	filters, msg := nodeFilters(req.InstanceInformationFilterList, req.Filters, req.MaxResults)
	if msg != "" {
		wire.WriteJSONError(w, http.StatusBadRequest, "ValidationException", msg)

		return
	}

	infos, err := nodes.DescribeInstanceInformation(r.Context(), filters)
	if err != nil {
		writeErr(w, err)

		return
	}

	maxResults := req.MaxResults
	if maxResults == 0 {
		maxResults = defaultNodesPage
	}

	start, end, next, ok := commandPage(w, req.NextToken, maxResults, maxResultsNodes, len(infos))
	if !ok {
		return
	}

	out := make([]instanceInformationJSON, 0, end-start)

	for i := start; i < end; i++ {
		in := &infos[i]
		out = append(out, instanceInformationJSON{
			InstanceID: in.InstanceID, PingStatus: in.PingStatus, LastPingDateTime: epoch(in.LastPingDateTime),
			AgentVersion: in.AgentVersion, IsLatestVersion: in.IsLatestVersion, PlatformType: in.PlatformType,
			PlatformName: in.PlatformName, PlatformVersion: in.PlatformVersion, ResourceType: in.ResourceType,
			IPAddress: in.IPAddress, ComputerName: in.ComputerName, SourceID: in.SourceID, SourceType: in.SourceType,
		})
	}

	wire.WriteJSON(w, map[string]any{"InstanceInformationList": out, "NextToken": omitEmpty(next)})
}

// nodeFilters merges the legacy and current DescribeInstanceInformation
// filter lists, returning a validation message for a request real SSM rejects.
func nodeFilters(legacy []instanceInfoFilterJSON, current []instanceStringFilterJSON,
	maxResults int32,
) (filters []ssmnative.InstanceInformationFilter, msg string) {
	if len(legacy) > 0 && len(current) > 0 {
		return nil, "You can use either InstanceInformationFilterList or Filters, but not both."
	}

	if maxResults != 0 && maxResults < minNodesPage {
		return nil, "1 validation error detected: Value '" + strconv.Itoa(int(maxResults)) +
			"' at 'maxResults' failed to satisfy constraint: Member must have value greater than or equal to 5"
	}

	filters = make([]ssmnative.InstanceInformationFilter, 0, len(current)+len(legacy))
	for _, f := range current {
		filters = append(filters, ssmnative.InstanceInformationFilter{Key: f.Key, Values: f.Values})
	}

	for _, f := range legacy {
		filters = append(filters, ssmnative.InstanceInformationFilter{Key: f.Key, Values: f.ValueSet})
	}

	return filters, ""
}
