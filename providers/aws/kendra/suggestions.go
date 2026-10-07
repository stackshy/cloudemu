package kendra

import (
	"context"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

// Query suggestion limits and the settings a fresh index starts with. Mode and the
// 180-day window are the documented defaults; the minimum user and query counts
// are the values the service starts with.
const (
	defaultLookBackDays     = 180
	defaultMinQueryingUsers = 8
	defaultMinQueryCount    = 5
	minSuggestionSetting    = 1
	maxSuggestionSetting    = 10000
	minSuggestionQueryRunes = 2
	maxSuggestionQueryRunes = 60
	minSuggestionWordRunes  = 5
	defaultMaxSuggestions   = 5
	maxSuggestionsPerCall   = 100
)

// queryLog counts the queries an index has served, which query suggestions are
// built from. It is guarded by its own mutex because Query is a read path.
type queryLog struct {
	mu     sync.Mutex
	counts map[string]map[string]int // indexID -> normalized query -> count
}

func (q *queryLog) add(indexID, query string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.counts == nil {
		q.counts = map[string]map[string]int{}
	}

	if q.counts[indexID] == nil {
		q.counts[indexID] = map[string]int{}
	}

	q.counts[indexID][query]++
}

func (q *queryLog) get(indexID string) map[string]int {
	q.mu.Lock()
	defer q.mu.Unlock()

	out := make(map[string]int, len(q.counts[indexID]))
	for k, v := range q.counts[indexID] {
		out[k] = v
	}

	return out
}

func (q *queryLog) clear(indexID string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	delete(q.counts, indexID)
}

// logQuery records a served query that qualifies for suggestions: it found at
// least one result and has a word longer than four characters.
func (m *Mock) logQuery(indexID, text string, results int) {
	text = strings.ToLower(strings.TrimSpace(text))
	if results == 0 || text == "" {
		return
	}

	for _, t := range tokenize(text) {
		if utf8.RuneCountInString(t) >= minSuggestionWordRunes {
			m.queryLog.add(indexID, text)

			return
		}
	}
}

func defaultSuggestionsConfig() driver.SuggestionsConfig {
	return driver.SuggestionsConfig{
		Mode: driver.SuggestionsEnabled, Status: driver.SuggestionsActive,
		QueryLogLookBackWindowInDays: defaultLookBackDays, IncludeQueriesWithoutUserInformation: true,
		MinimumNumberOfQueryingUsers: defaultMinQueryingUsers, MinimumQueryCount: defaultMinQueryCount,
	}
}

func (m *Mock) suggestionsConfig(indexID string) driver.SuggestionsConfig {
	cfg, ok := m.suggestions.Get(indexID)
	if !ok {
		cfg = defaultSuggestionsConfig()
	}

	cfg.AttributeSuggestionsConfig = copyRaw(cfg.AttributeSuggestionsConfig)

	return cfg
}

// DescribeQuerySuggestionsConfig returns an index's query suggestions settings.
// Every index starts with suggestions ENABLED and ACTIVE.
func (m *Mock) DescribeQuerySuggestionsConfig(_ context.Context, indexID string) (*driver.SuggestionsConfig, error) {
	if _, err := m.getIndex(indexID); err != nil {
		return nil, err
	}

	cfg := m.suggestionsConfig(indexID)
	cfg.Status = m.settleStatus(suggestionsKey(indexID), cfg.Status)
	cfg.TotalSuggestionsCount = int32(len(m.eligibleQueries(indexID, &cfg))) //nolint:gosec // bounded by the in-memory query log

	return &cfg, nil
}

func suggestionsKey(indexID string) string { return indexID + "/suggestions" }

func checkSetting(name string, v *int32) error {
	if v != nil && (*v < minSuggestionSetting || *v > maxSuggestionSetting) {
		return validation("%s must be between %d and %d", name, minSuggestionSetting, maxSuggestionSetting)
	}

	return nil
}

// UpdateQuerySuggestionsConfig applies the supplied settings, leaving omitted
// ones unchanged. Settings still applying (UPDATING under async settling)
// conflict with another update.
func (m *Mock) UpdateQuerySuggestionsConfig(_ context.Context, in *driver.UpdateSuggestionsConfigInput) error {
	if err := validateSuggestionsUpdate(in); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireActiveIndex(in.IndexID); err != nil {
		return err
	}

	cfg := m.suggestionsConfig(in.IndexID)

	if status := m.settleStatus(suggestionsKey(in.IndexID), cfg.Status); status != driver.SuggestionsActive {
		return conflict("query suggestions settings for index %q are still updating", in.IndexID)
	}

	applySuggestionsUpdate(&cfg, in)
	cfg.LastSuggestionsBuildTime = m.now()
	m.suggestions.Set(in.IndexID, cfg)
	m.beginSettle(suggestionsKey(in.IndexID), driver.SuggestionsUpdating)

	return nil
}

// validateSuggestionsUpdate applies UpdateQuerySuggestionsConfig's input rules.
func validateSuggestionsUpdate(in *driver.UpdateSuggestionsConfigInput) error {
	if err := validateIndexID(in.IndexID); err != nil {
		return err
	}

	if in.Mode != nil && *in.Mode != driver.SuggestionsEnabled && *in.Mode != driver.SuggestionsLearnOnly {
		return validation("invalid Mode: %q", *in.Mode)
	}

	if err := checkSetting("MinimumNumberOfQueryingUsers", in.MinimumNumberOfQueryingUsers); err != nil {
		return err
	}

	if err := checkSetting("MinimumQueryCount", in.MinimumQueryCount); err != nil {
		return err
	}

	if in.QueryLogLookBackWindowInDays != nil && *in.QueryLogLookBackWindowInDays < 1 {
		return validation("QueryLogLookBackWindowInDays must be at least 1")
	}

	return nil
}

func applySuggestionsUpdate(cfg *driver.SuggestionsConfig, in *driver.UpdateSuggestionsConfigInput) {
	if in.Mode != nil {
		cfg.Mode = *in.Mode
	}

	if in.QueryLogLookBackWindowInDays != nil {
		cfg.QueryLogLookBackWindowInDays = *in.QueryLogLookBackWindowInDays
	}

	if in.IncludeQueriesWithoutUserInformation != nil {
		cfg.IncludeQueriesWithoutUserInformation = *in.IncludeQueriesWithoutUserInformation
	}

	if in.MinimumNumberOfQueryingUsers != nil {
		cfg.MinimumNumberOfQueryingUsers = *in.MinimumNumberOfQueryingUsers
	}

	if in.MinimumQueryCount != nil {
		cfg.MinimumQueryCount = *in.MinimumQueryCount
	}

	if in.AttributeSuggestionsConfig != nil {
		cfg.AttributeSuggestionsConfig = copyRaw(in.AttributeSuggestionsConfig)
	}
}

// ClearQuerySuggestions deletes the learned suggestions (the logged queries) and
// stamps LastClearTime.
func (m *Mock) ClearQuerySuggestions(_ context.Context, indexID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireActiveIndex(indexID); err != nil {
		return err
	}

	cfg := m.suggestionsConfig(indexID)
	cfg.LastClearTime = m.now()
	m.suggestions.Set(indexID, cfg)
	m.queryLog.clear(indexID)

	return nil
}

// eligibleQueries are the logged queries searched at least MinimumQueryCount
// times. User counts are not tracked, so MinimumNumberOfQueryingUsers is not
// applied.
func (m *Mock) eligibleQueries(indexID string, cfg *driver.SuggestionsConfig) []string {
	out := []string{}

	for q, n := range m.queryLog.get(indexID) {
		if int32(n) >= cfg.MinimumQueryCount { //nolint:gosec // counts are bounded by in-memory queries
			out = append(out, q)
		}
	}

	sort.Strings(out)

	return out
}

// GetQuerySuggestions returns the logged queries that start with the text the
// user has typed (2-60 characters), most searched first. In LEARN_ONLY mode, for
// DOCUMENT_ATTRIBUTES suggestions (not generated) or before enough queries have
// been served it returns none.
func (m *Mock) GetQuerySuggestions(_ context.Context, in *driver.GetSuggestionsInput) (*driver.SuggestionsResult, error) {
	if err := validateGetSuggestions(in); err != nil {
		return nil, err
	}

	if _, err := m.getIndex(in.IndexID); err != nil {
		return nil, err
	}

	out := &driver.SuggestionsResult{QuerySuggestionsID: newUUID(), Suggestions: []driver.Suggestion{}}
	cfg := m.suggestionsConfig(in.IndexID)
	typed := strings.ToLower(in.QueryText)
	runes := utf8.RuneCountInString(typed)

	if !suggestionsApply(&cfg, in, runes) {
		return out, nil
	}

	limit := int(in.MaxSuggestionsCount)
	if limit == 0 {
		limit = defaultMaxSuggestions
	}

	counts := m.queryLog.get(in.IndexID)
	candidates := []string{}

	for _, q := range m.eligibleQueries(in.IndexID, &cfg) {
		if strings.HasPrefix(q, typed) {
			candidates = append(candidates, q)
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool { return counts[candidates[i]] > counts[candidates[j]] })

	for _, q := range candidates[:min(limit, len(candidates))] {
		out.Suggestions = append(out.Suggestions, driver.Suggestion{
			ID: newUUID(), Text: q,
			Highlights: []driver.Highlight{{BeginOffset: 0, EndOffset: int32(runes)}}, //nolint:gosec // bounded by the 60-rune limit
		})
	}

	return out, nil
}

// validateGetSuggestions applies GetQuerySuggestions' input rules.
func validateGetSuggestions(in *driver.GetSuggestionsInput) error {
	if err := validateIndexID(in.IndexID); err != nil {
		return err
	}

	for _, t := range in.SuggestionTypes {
		if t != "QUERY" && t != "DOCUMENT_ATTRIBUTES" {
			return validation("invalid SuggestionTypes value: %q", t)
		}
	}

	if in.MaxSuggestionsCount < 0 || in.MaxSuggestionsCount > maxSuggestionsPerCall {
		return validation("MaxSuggestionsCount must be between 1 and %d", maxSuggestionsPerCall)
	}

	return nil
}

// suggestionsApply reports whether suggestions are produced at all: ENABLED
// mode, a 2-60 character prefix and QUERY suggestions requested (or no type
// given).
func suggestionsApply(cfg *driver.SuggestionsConfig, in *driver.GetSuggestionsInput, runes int) bool {
	if cfg.Mode != driver.SuggestionsEnabled || runes < minSuggestionQueryRunes || runes > maxSuggestionQueryRunes {
		return false
	}

	return len(in.SuggestionTypes) == 0 || containsStr(in.SuggestionTypes, "QUERY")
}

func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}

	return false
}
