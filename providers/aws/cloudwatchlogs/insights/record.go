package insights

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"
)

// timestampLayout is how Logs Insights renders @timestamp and @ingestionTime.
const timestampLayout = "2006-01-02 15:04:05.000"

// Event is a log event as the query engine sees it.
type Event struct {
	Account   string
	LogGroup  string
	LogStream string
	Timestamp time.Time
	Ingestion time.Time
	Message   string
	Ptr       string
}

// NewRecord builds an event's fields: the system fields plus, for a JSON
// message, its properties flattened to dotted names (arrays by index), the
// way Logs Insights discovers fields in JSON logs.
func NewRecord(e *Event) Record {
	r := Record{
		FieldTimestamp:     e.Timestamp.UTC().Format(timestampLayout),
		FieldMessage:       e.Message,
		FieldLogStream:     e.LogStream,
		FieldLog:           e.Account + ":" + e.LogGroup,
		FieldPtr:           e.Ptr,
		FieldIngestionTime: e.Ingestion.UTC().Format(timestampLayout),
	}

	var doc any
	if strings.HasPrefix(strings.TrimSpace(e.Message), "{") && json.Unmarshal([]byte(e.Message), &doc) == nil {
		flatten(r, "", doc)
	}

	return r
}

func flatten(r Record, prefix string, v any) {
	join := func(k string) string {
		if prefix == "" {
			return k
		}

		return prefix + "." + k
	}

	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			flatten(r, join(k), child)
		}
	case []any:
		for i, child := range x {
			flatten(r, join(strconv.Itoa(i)), child)
		}
	case nil:
		// JSON null is not a value; the field is left out.
	default:
		if prefix != "" {
			r[prefix] = x
		}
	}
}

// Strings renders a record's fields as strings, the GetLogRecord shape.
func (r Record) Strings() map[string]string {
	out := make(map[string]string, len(r))

	for k, v := range r {
		out[k] = toStr(v)
	}

	return out
}

// FieldShare is how often a field occurs across a set of events.
type FieldShare struct {
	Name    string
	Percent int
}

// percentScale turns a fraction into a whole percentage.
const percentScale = 100

// FieldShares returns every field that occurs in recs with the percentage
// of records that carry it, most common first. @ptr is internal and left out.
func FieldShares(recs []Record) []FieldShare {
	counts := map[string]int{}

	for _, r := range recs {
		for k := range r {
			if k != FieldPtr {
				counts[k]++
			}
		}
	}

	out := make([]FieldShare, 0, len(counts))
	for k, n := range counts {
		out = append(out, FieldShare{Name: k, Percent: n * percentScale / len(recs)})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Percent != out[j].Percent {
			return out[i].Percent > out[j].Percent
		}

		return out[i].Name < out[j].Name
	})

	return out
}
