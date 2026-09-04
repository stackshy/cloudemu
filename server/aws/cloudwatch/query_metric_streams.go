package cloudwatch

// This file adds the classic AWS query-protocol path (form-encoded POST,
// Action=..., XML responses) for the CloudWatch metric-stream operations and
// their tags, so `aws cloudwatch ...` and Terraform's aws_cloudwatch_metric_stream
// resource — both of which still speak query protocol for CloudWatch, unlike
// the modern rpc-v2-cbor path used by current aws-sdk-go-v2 — work against the
// emulator. See query.go for the shared query-protocol plumbing.

import (
	"context"
	"encoding/xml"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// writeMetricStreamQueryDriverErr is the query-protocol counterpart of
// writeMetricStreamDriverErr (see metric_streams.go): it maps a metric-stream
// driver error to CloudWatch's real ResourceNotFoundException /
// InvalidParameterValueException error codes rather than the shorter names
// the shared writeQueryDriverErr uses for the older alarm operations.
func writeMetricStreamQueryDriverErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeQueryError(w, http.StatusNotFound, "ResourceNotFoundException", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeQueryError(w, http.StatusBadRequest, "InvalidParameterValueException", err.Error())
	default:
		writeQueryDriverErr(w, err)
	}
}

func (h *Handler) queryPutMetricStream(w http.ResponseWriter, r *http.Request) {
	store, ok := h.monitoring.(metricStreamStore)
	if !ok {
		writeQueryError(w, http.StatusBadRequest, "InvalidAction", "metric streams not supported")
		return
	}

	arn, err := store.PutMetricStream(r.Context(), mondriver.MetricStreamConfig{
		Name:                         r.Form.Get("Name"),
		FirehoseARN:                  r.Form.Get("FirehoseArn"),
		RoleARN:                      r.Form.Get("RoleArn"),
		OutputFormat:                 r.Form.Get("OutputFormat"),
		IncludeFilters:               queryMetricStreamFilters(r, "IncludeFilters.member."),
		ExcludeFilters:               queryMetricStreamFilters(r, "ExcludeFilters.member."),
		StatisticsConfigurations:     queryMetricStreamStatisticsConfigs(r, "StatisticsConfigurations.member."),
		IncludeLinkedAccountsMetrics: r.Form.Get("IncludeLinkedAccountsMetrics") == "true",
		Tags:                         queryTagPairs(r, "Tags.member."),
	})
	if err != nil {
		writeMetricStreamQueryDriverErr(w, err)
		return
	}

	writeQueryResponse(w, "PutMetricStreamResponse", putMetricStreamResultXML{Arn: arn})
}

func (h *Handler) queryGetMetricStream(w http.ResponseWriter, r *http.Request) {
	store, ok := h.monitoring.(metricStreamStore)
	if !ok {
		writeQueryError(w, http.StatusBadRequest, "InvalidAction", "metric streams not supported")
		return
	}

	s, err := store.GetMetricStream(r.Context(), r.Form.Get("Name"))
	if err != nil {
		writeMetricStreamQueryDriverErr(w, err)
		return
	}

	writeQueryResponse(w, "GetMetricStreamResponse", getMetricStreamResultXML{
		Arn:                          s.ARN,
		Name:                         s.Name,
		FirehoseArn:                  s.FirehoseARN,
		RoleArn:                      s.RoleARN,
		OutputFormat:                 s.OutputFormat,
		State:                        s.State,
		IncludeFilters:               toMetricStreamFilterMembers(s.IncludeFilters),
		ExcludeFilters:               toMetricStreamFilterMembers(s.ExcludeFilters),
		StatisticsConfigurations:     toMetricStreamStatisticsConfigMembers(s.StatisticsConfigurations),
		IncludeLinkedAccountsMetrics: s.IncludeLinkedAccountsMetrics,
		CreationDate:                 s.CreationDate.UTC().Format(time.RFC3339),
		LastUpdateDate:               s.LastUpdateDate.UTC().Format(time.RFC3339),
	})
}

func (h *Handler) queryListMetricStreams(w http.ResponseWriter, r *http.Request) {
	store, ok := h.monitoring.(metricStreamStore)
	if !ok {
		writeQueryError(w, http.StatusBadRequest, "InvalidAction", "metric streams not supported")
		return
	}

	entries, err := store.ListMetricStreams(r.Context())
	if err != nil {
		writeMetricStreamQueryDriverErr(w, err)
		return
	}

	size := listMetricStreamsPageSize
	if v, _ := strconv.Atoi(r.Form.Get("MaxResults")); v > 0 && v < size {
		size = v
	}

	from, to, next := pageWindow(len(entries), decodeOffsetToken(r.Form.Get("NextToken")), size)

	members := make([]metricStreamEntryXML, 0, to-from)

	for i := from; i < to; i++ {
		e := &entries[i]
		members = append(members, metricStreamEntryXML{
			Arn:            e.ARN,
			Name:           e.Name,
			FirehoseArn:    e.FirehoseARN,
			OutputFormat:   e.OutputFormat,
			State:          e.State,
			CreationDate:   e.CreationDate.UTC().Format(time.RFC3339),
			LastUpdateDate: e.LastUpdateDate.UTC().Format(time.RFC3339),
		})
	}

	result := listMetricStreamsResultXML{Entries: members}
	if next > 0 {
		result.NextToken = encodeOffsetToken(next)
	}

	writeQueryResponse(w, "ListMetricStreamsResponse", result)
}

func (h *Handler) queryDeleteMetricStream(w http.ResponseWriter, r *http.Request) {
	store, ok := h.monitoring.(metricStreamStore)
	if !ok {
		writeQueryError(w, http.StatusBadRequest, "InvalidAction", "metric streams not supported")
		return
	}

	if err := store.DeleteMetricStream(r.Context(), r.Form.Get("Name")); err != nil {
		writeMetricStreamQueryDriverErr(w, err)
		return
	}

	writeQueryResponse(w, "DeleteMetricStreamResponse", emptyQueryResult("DeleteMetricStreamResult"))
}

func (h *Handler) queryStartMetricStreams(w http.ResponseWriter, r *http.Request) {
	store, ok := h.monitoring.(metricStreamStore)
	if !ok {
		writeQueryError(w, http.StatusBadRequest, "InvalidAction", "metric streams not supported")
		return
	}

	if err := store.StartMetricStreams(r.Context(), queryStringList(r, "Names.member.")); err != nil {
		writeMetricStreamQueryDriverErr(w, err)
		return
	}

	writeQueryResponse(w, "StartMetricStreamsResponse", emptyQueryResult("StartMetricStreamsResult"))
}

func (h *Handler) queryStopMetricStreams(w http.ResponseWriter, r *http.Request) {
	store, ok := h.monitoring.(metricStreamStore)
	if !ok {
		writeQueryError(w, http.StatusBadRequest, "InvalidAction", "metric streams not supported")
		return
	}

	if err := store.StopMetricStreams(r.Context(), queryStringList(r, "Names.member.")); err != nil {
		writeMetricStreamQueryDriverErr(w, err)
		return
	}

	writeQueryResponse(w, "StopMetricStreamsResponse", emptyQueryResult("StopMetricStreamsResult"))
}

// errTaggingUnsupported signals that the monitoring backend behind h doesn't
// implement the tag capability a routed-to ARN needs (alarmTagger or
// metricStreamTagger).
var errTaggingUnsupported = errors.New("tagging not supported")

// queryTagResource, queryUntagResource, and queryListTagsForResource route to
// the metric-stream tagger when ResourceARN names a metric stream, and to the
// alarm tagger otherwise — mirroring the rpc-v2-cbor tagResource/untagResource/
// listTagsForResource dispatch in metric_data_ops.go. Each delegates to a
// small ARN-routing helper so the two resource kinds' near-identical bodies
// aren't duplicated per operation.
func (h *Handler) queryTagResource(w http.ResponseWriter, r *http.Request) {
	arn := r.Form.Get("ResourceARN")

	if err := h.addResourceTagsByARN(r.Context(), arn, queryTagPairs(r, "Tags.member.")); err != nil {
		writeTagRouteQueryErr(w, arn, err)
		return
	}

	writeQueryResponse(w, "TagResourceResponse", emptyQueryResult("TagResourceResult"))
}

func (h *Handler) queryUntagResource(w http.ResponseWriter, r *http.Request) {
	arn := r.Form.Get("ResourceARN")

	if err := h.removeResourceTagsByARN(r.Context(), arn, queryStringList(r, "TagKeys.member.")); err != nil {
		writeTagRouteQueryErr(w, arn, err)
		return
	}

	writeQueryResponse(w, "UntagResourceResponse", emptyQueryResult("UntagResourceResult"))
}

func (h *Handler) queryListTagsForResource(w http.ResponseWriter, r *http.Request) {
	arn := r.Form.Get("ResourceARN")

	tags, err := h.resourceTagsByARN(r.Context(), arn)
	if err != nil {
		writeTagRouteQueryErr(w, arn, err)
		return
	}

	writeQueryResponse(w, "ListTagsForResourceResponse", listTagsForResourceResultXML{Tags: toTagMemberXMLs(tags)})
}

// addResourceTagsByARN, removeResourceTagsByARN, and resourceTagsByARN route
// arn to the metric-stream tagger when it names a metric stream, and to the
// alarm tagger otherwise (including a bare alarm name, for backward
// compatibility). They return errTaggingUnsupported when the monitoring
// backend doesn't implement the needed capability.
func (h *Handler) addResourceTagsByARN(ctx context.Context, arn string, tags map[string]string) error {
	if name, ok := metricStreamNameFromARN(arn); ok {
		tagger, ok := h.monitoring.(metricStreamTagger)
		if !ok {
			return errTaggingUnsupported
		}

		return tagger.AddMetricStreamTags(ctx, name, tags)
	}

	tagger, ok := h.monitoring.(alarmTagger)
	if !ok {
		return errTaggingUnsupported
	}

	return tagger.AddAlarmTags(ctx, alarmNameFromARN(arn), tags)
}

func (h *Handler) removeResourceTagsByARN(ctx context.Context, arn string, keys []string) error {
	if name, ok := metricStreamNameFromARN(arn); ok {
		tagger, ok := h.monitoring.(metricStreamTagger)
		if !ok {
			return errTaggingUnsupported
		}

		return tagger.RemoveMetricStreamTags(ctx, name, keys)
	}

	tagger, ok := h.monitoring.(alarmTagger)
	if !ok {
		return errTaggingUnsupported
	}

	return tagger.RemoveAlarmTags(ctx, alarmNameFromARN(arn), keys)
}

func (h *Handler) resourceTagsByARN(ctx context.Context, arn string) (map[string]string, error) {
	if name, ok := metricStreamNameFromARN(arn); ok {
		tagger, ok := h.monitoring.(metricStreamTagger)
		if !ok {
			return nil, errTaggingUnsupported
		}

		return tagger.MetricStreamTags(ctx, name)
	}

	tagger, ok := h.monitoring.(alarmTagger)
	if !ok {
		return nil, errTaggingUnsupported
	}

	return tagger.AlarmTags(ctx, alarmNameFromARN(arn))
}

// writeTagRouteQueryErr writes the query-protocol response for a tag-routing
// error: an unsupported-capability error becomes InvalidAction, and any other
// error is mapped by the metric-stream or alarm driver-error mapper depending
// on which resource kind arn routed to.
func writeTagRouteQueryErr(w http.ResponseWriter, arn string, err error) {
	if errors.Is(err, errTaggingUnsupported) {
		writeQueryError(w, http.StatusBadRequest, "InvalidAction", err.Error())
		return
	}

	if _, ok := metricStreamNameFromARN(arn); ok {
		writeMetricStreamQueryDriverErr(w, err)
		return
	}

	writeQueryDriverErr(w, err)
}

// ---- form parsing helpers ----

// queryMetricStreamFilters parses a MetricStreamFilter list (Namespace +
// nested MetricNames) at the given "Xxx.member." prefix.
func queryMetricStreamFilters(r *http.Request, prefix string) []mondriver.MetricStreamFilter {
	var out []mondriver.MetricStreamFilter

	for i := 1; ; i++ {
		p := prefix + strconv.Itoa(i) + "."

		ns := r.Form.Get(p + "Namespace")
		names := queryStringList(r, p+"MetricNames.member.")

		if ns == "" && len(names) == 0 {
			break
		}

		out = append(out, mondriver.MetricStreamFilter{Namespace: ns, MetricNames: names})
	}

	return out
}

// queryMetricStreamStatisticsConfigs parses a MetricStreamStatisticsConfiguration
// list at the given "Xxx.member." prefix.
func queryMetricStreamStatisticsConfigs(r *http.Request, prefix string) []mondriver.MetricStreamStatisticsConfig {
	var out []mondriver.MetricStreamStatisticsConfig

	for i := 1; ; i++ {
		p := prefix + strconv.Itoa(i) + "."

		additional := queryStringList(r, p+"AdditionalStatistics.member.")

		var metrics []mondriver.MetricStreamStatisticsMetric

		for j := 1; ; j++ {
			mp := p + "IncludeMetrics.member." + strconv.Itoa(j) + "."

			ns := r.Form.Get(mp + "Namespace")
			name := r.Form.Get(mp + "MetricName")

			if ns == "" && name == "" {
				break
			}

			metrics = append(metrics, mondriver.MetricStreamStatisticsMetric{Namespace: ns, MetricName: name})
		}

		if len(metrics) == 0 && len(additional) == 0 {
			break
		}

		out = append(out, mondriver.MetricStreamStatisticsConfig{IncludeMetrics: metrics, AdditionalStatistics: additional})
	}

	return out
}

// queryTagPairs parses a Tag{Key,Value} list at the given "Xxx.member." prefix.
func queryTagPairs(r *http.Request, prefix string) map[string]string {
	var out map[string]string

	for i := 1; ; i++ {
		p := prefix + strconv.Itoa(i) + "."

		key := r.Form.Get(p + "Key")
		if key == "" {
			break
		}

		if out == nil {
			out = map[string]string{}
		}

		out[key] = r.Form.Get(p + "Value")
	}

	return out
}

func toTagMemberXMLs(tags map[string]string) []tagMemberXML {
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	out := make([]tagMemberXML, 0, len(keys))
	for _, k := range keys {
		out = append(out, tagMemberXML{Key: k, Value: tags[k]})
	}

	return out
}

// ---- XML request/response shapes (query protocol, 2010-08-01) ----

// emptyQueryResult renders a nameless <XxxResult/> element for an operation
// whose response carries no fields. The query-protocol deserializer still
// requires that element to be present — e.g. DeleteMetricStream fails to
// deserialize a response with no DeleteMetricStreamResult node at all — even
// though the operation returns no data.
func emptyQueryResult(name string) any {
	return struct {
		XMLName xml.Name
	}{XMLName: xml.Name{Local: name}}
}

type putMetricStreamResultXML struct {
	XMLName xml.Name `xml:"PutMetricStreamResult"`
	Arn     string   `xml:"Arn"`
}

type metricStreamFilterMemberXML struct {
	Namespace   string   `xml:"Namespace,omitempty"`
	MetricNames []string `xml:"MetricNames>member,omitempty"`
}

type metricStreamStatisticsMetricMemberXML struct {
	Namespace  string `xml:"Namespace,omitempty"`
	MetricName string `xml:"MetricName,omitempty"`
}

type metricStreamStatisticsConfigMemberXML struct {
	IncludeMetrics       []metricStreamStatisticsMetricMemberXML `xml:"IncludeMetrics>member,omitempty"`
	AdditionalStatistics []string                                `xml:"AdditionalStatistics>member,omitempty"`
}

type getMetricStreamResultXML struct {
	XMLName                      xml.Name                                `xml:"GetMetricStreamResult"`
	Arn                          string                                  `xml:"Arn,omitempty"`
	Name                         string                                  `xml:"Name,omitempty"`
	FirehoseArn                  string                                  `xml:"FirehoseArn,omitempty"`
	RoleArn                      string                                  `xml:"RoleArn,omitempty"`
	OutputFormat                 string                                  `xml:"OutputFormat,omitempty"`
	State                        string                                  `xml:"State,omitempty"`
	IncludeFilters               []metricStreamFilterMemberXML           `xml:"IncludeFilters>member,omitempty"`
	ExcludeFilters               []metricStreamFilterMemberXML           `xml:"ExcludeFilters>member,omitempty"`
	StatisticsConfigurations     []metricStreamStatisticsConfigMemberXML `xml:"StatisticsConfigurations>member,omitempty"`
	IncludeLinkedAccountsMetrics bool                                    `xml:"IncludeLinkedAccountsMetrics,omitempty"`
	CreationDate                 string                                  `xml:"CreationDate,omitempty"`
	LastUpdateDate               string                                  `xml:"LastUpdateDate,omitempty"`
}

type metricStreamEntryXML struct {
	Arn            string `xml:"Arn,omitempty"`
	Name           string `xml:"Name,omitempty"`
	FirehoseArn    string `xml:"FirehoseArn,omitempty"`
	OutputFormat   string `xml:"OutputFormat,omitempty"`
	State          string `xml:"State,omitempty"`
	CreationDate   string `xml:"CreationDate,omitempty"`
	LastUpdateDate string `xml:"LastUpdateDate,omitempty"`
}

type listMetricStreamsResultXML struct {
	XMLName   xml.Name               `xml:"ListMetricStreamsResult"`
	Entries   []metricStreamEntryXML `xml:"Entries>member"`
	NextToken string                 `xml:"NextToken,omitempty"`
}

type tagMemberXML struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

type listTagsForResourceResultXML struct {
	XMLName xml.Name       `xml:"ListTagsForResourceResult"`
	Tags    []tagMemberXML `xml:"Tags>member"`
}

func toMetricStreamFilterMembers(in []mondriver.MetricStreamFilter) []metricStreamFilterMemberXML {
	if len(in) == 0 {
		return nil
	}

	out := make([]metricStreamFilterMemberXML, 0, len(in))
	for _, f := range in {
		out = append(out, metricStreamFilterMemberXML{Namespace: f.Namespace, MetricNames: f.MetricNames})
	}

	return out
}

func toMetricStreamStatisticsConfigMembers(in []mondriver.MetricStreamStatisticsConfig) []metricStreamStatisticsConfigMemberXML {
	if len(in) == 0 {
		return nil
	}

	out := make([]metricStreamStatisticsConfigMemberXML, 0, len(in))

	for _, c := range in {
		metrics := make([]metricStreamStatisticsMetricMemberXML, 0, len(c.IncludeMetrics))
		for _, mtr := range c.IncludeMetrics {
			metrics = append(metrics, metricStreamStatisticsMetricMemberXML{Namespace: mtr.Namespace, MetricName: mtr.MetricName})
		}

		out = append(out, metricStreamStatisticsConfigMemberXML{
			IncludeMetrics:       metrics,
			AdditionalStatistics: c.AdditionalStatistics,
		})
	}

	return out
}
