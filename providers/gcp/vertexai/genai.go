package vertexai

import (
	"context"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/vertexai/driver"
)

// approxTokens is a deterministic, whitespace-based token estimate.
func approxTokens(text string) int {
	if strings.TrimSpace(text) == "" {
		return 0
	}

	return len(strings.Fields(text))
}

// GenerateContent returns a deterministic emulated Gemini response: it echoes
// the last user turn as the model reply and reports plausible token usage.
//

func (m *Mock) GenerateContent(
	_ context.Context, model string, req driver.GenerateContentRequest,
) (*driver.GenerateContentResponse, error) {
	if model == "" {
		return nil, errors.New(errors.InvalidArgument, "model is required")
	}

	prompt := lastUserText(req.Contents)
	reply := "Emulated response to: " + prompt

	promptTokens := approxTokens(prompt)
	outTokens := approxTokens(reply)

	m.emitMetric("generate_content/count", 1, map[string]string{"model": model})

	return &driver.GenerateContentResponse{
		Candidates: []driver.Candidate{{
			Content:      driver.Content{Role: "model", Parts: []driver.Part{{Text: reply}}},
			FinishReason: "STOP",
		}},
		UsageMetadata: driver.UsageMetadata{
			PromptTokenCount:     promptTokens,
			CandidatesTokenCount: outTokens,
			TotalTokenCount:      promptTokens + outTokens,
		},
	}, nil
}

func (*Mock) CountTokens(_ context.Context, model string, req driver.GenerateContentRequest) (*driver.CountTokensResponse, error) {
	if model == "" {
		return nil, errors.New(errors.InvalidArgument, "model is required")
	}

	total := 0

	for _, c := range req.Contents {
		for _, p := range c.Parts {
			total += approxTokens(p.Text)
		}
	}

	return &driver.CountTokensResponse{TotalTokens: total}, nil
}

func lastUserText(contents []driver.Content) string {
	for i := len(contents) - 1; i >= 0; i-- {
		if contents[i].Role == "user" || contents[i].Role == "" {
			var b strings.Builder
			for _, p := range contents[i].Parts {
				b.WriteString(p.Text)
			}

			return b.String()
		}
	}

	return ""
}

// --- Tuning jobs (synchronous create) ---

func (m *Mock) CreateTuningJob(_ context.Context, cfg driver.TuningJobConfig) (*driver.TuningJob, error) {
	now := m.now()
	name := m.resName(cfg.Location, "tuningJobs", m.newID())

	tuned := cfg.TunedModelName
	if tuned == "" {
		tuned = m.resName(cfg.Location, "models", m.newID())
	}

	job := &driver.TuningJob{
		Name:           name,
		BaseModel:      cfg.BaseModel,
		State:          driver.JobStateSucceeded,
		TunedModelName: tuned,
		Endpoint:       m.resName(cfg.Location, "endpoints", m.newID()),
		CreateTime:     now,
		EndTime:        now,
	}
	m.tuningJobs.Set(name, job)

	out := *job

	return &out, nil
}

func (m *Mock) GetTuningJob(_ context.Context, name string) (*driver.TuningJob, error) {
	j, ok := m.tuningJobs.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "tuning job %q not found", name)
	}

	out := *j

	return &out, nil
}

func (m *Mock) ListTuningJobs(_ context.Context, location string) ([]driver.TuningJob, error) {
	out := make([]driver.TuningJob, 0)

	for _, j := range m.tuningJobs.All() {
		if location == "" || locationOf(j.Name) == location {
			out = append(out, *j)
		}
	}

	return out, nil
}

func (m *Mock) CancelTuningJob(_ context.Context, name string) error {
	j, ok := m.tuningJobs.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "tuning job %q not found", name)
	}

	updated := *j
	updated.State = driver.JobStateCancelled
	m.tuningJobs.Set(name, &updated)

	return nil
}

// --- Cached contents (synchronous CRUD) ---

//nolint:gocritic // cfg matches the driver signature; copied on entry.
func (m *Mock) CreateCachedContent(_ context.Context, cfg driver.CachedContentConfig) (*driver.CachedContent, error) {
	if cfg.Model == "" {
		return nil, errors.New(errors.InvalidArgument, "model is required")
	}

	ttl := cfg.TTLSeconds
	if ttl <= 0 {
		ttl = defaultCacheTTLSeconds
	}

	created := m.opts.Clock.Now().UTC()
	expire := created.Add(time.Duration(ttl) * time.Second)

	name := m.resName(cfg.Location, "cachedContents", m.newID())
	cc := &driver.CachedContent{
		Name:              name,
		Model:             cfg.Model,
		DisplayName:       cfg.DisplayName,
		Contents:          cloneContents(cfg.Contents),
		SystemInstruction: cloneContent(cfg.SystemInstruction),
		CreateTime:        created.Format(time.RFC3339),
		ExpireTime:        expire.Format(time.RFC3339),
	}
	m.cachedContent.Set(name, cc)

	return cloneCachedContent(cc), nil
}

// defaultCacheTTLSeconds matches Vertex's default context-cache lifetime (1h)
// when the caller omits an explicit TTL.
const defaultCacheTTLSeconds = 3600

func cloneContents(in []driver.Content) []driver.Content {
	if in == nil {
		return nil
	}

	out := make([]driver.Content, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].Parts = append([]driver.Part(nil), in[i].Parts...)
	}

	return out
}

func cloneContent(in *driver.Content) *driver.Content {
	if in == nil {
		return nil
	}

	out := *in
	out.Parts = append([]driver.Part(nil), in.Parts...)

	return &out
}

func cloneCachedContent(in *driver.CachedContent) *driver.CachedContent {
	out := *in
	out.Contents = cloneContents(in.Contents)
	out.SystemInstruction = cloneContent(in.SystemInstruction)

	return &out
}

func (m *Mock) GetCachedContent(_ context.Context, name string) (*driver.CachedContent, error) {
	cc, ok := m.cachedContent.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "cached content %q not found", name)
	}

	return cloneCachedContent(cc), nil
}

func (m *Mock) ListCachedContents(_ context.Context, location string) ([]driver.CachedContent, error) {
	out := make([]driver.CachedContent, 0)

	for _, cc := range m.cachedContent.All() {
		if location == "" || locationOf(cc.Name) == location {
			out = append(out, *cloneCachedContent(cc))
		}
	}

	return out, nil
}

func (m *Mock) DeleteCachedContent(_ context.Context, name string) error {
	if !m.cachedContent.Has(name) {
		return errors.Newf(errors.NotFound, "cached content %q not found", name)
	}

	m.cachedContent.Delete(name)

	return nil
}
