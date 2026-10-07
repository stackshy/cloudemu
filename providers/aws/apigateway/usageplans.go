package apigateway

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

var _ driver.UsagePlans = (*Mock)(nil)

const (
	msgUsagePlanNotFound    = "Invalid Usage Plan ID specified"
	msgUsagePlanKeyNotFound = "Invalid Usage Plan Key identifier specified"
	msgUsagePlanKeyExists   = "API Key already attached to the usage plan"
	msgUsagePlanName        = "Name is required"
	msgKeyType              = "Invalid key type specified: only API_KEY is supported"
	msgQuota                = "Quota limit must be at least 0 and the period DAY, WEEK or MONTH"
	msgThrottle             = "Throttle rate and burst limits must not be negative"
	pathQuotaLimit          = "/quota/limit"
	keyTypeAPIKey           = "API_KEY"
	daysInWeek              = 7
)

// CreateUsagePlan creates a usage plan. Every API stage it names must exist.
func (m *Mock) CreateUsagePlan(_ context.Context, in *driver.CreateUsagePlanInput) (*driver.UsagePlan, error) {
	if in.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, msgUsagePlanName)
	}

	if err := validateQuotaThrottle(in.Quota, in.Throttle); err != nil {
		return nil, err
	}

	for _, st := range in.APIStages {
		if err := m.checkPlanStage(st); err != nil {
			return nil, err
		}
	}

	plan := &driver.UsagePlan{
		ID: genID(), Name: in.Name, Description: in.Description, APIStages: copyPlanStages(in.APIStages),
		Throttle: copyThrottle(in.Throttle), Quota: copyQuota(in.Quota), Tags: copyStrMap(in.Tags),
	}

	m.regionMu.Lock()
	m.plans[plan.ID] = plan
	m.planKeys[plan.ID] = map[string]bool{}
	m.regionMu.Unlock()

	out := copyPlan(plan)

	return &out, nil
}

func validateQuotaThrottle(q *driver.QuotaSettings, t *driver.ThrottleSettings) error {
	if q != nil {
		if q.Limit < 0 || q.Offset < 0 || (q.Period != driver.QuotaDay && q.Period != driver.QuotaWeek && q.Period != driver.QuotaMonth) {
			return cerrors.New(cerrors.InvalidArgument, msgQuota)
		}
	}

	if t != nil && (t.BurstLimit < 0 || t.RateLimit < 0) {
		return cerrors.New(cerrors.InvalidArgument, msgThrottle)
	}

	return nil
}

// checkPlanStage validates that an API stage a plan names exists.
func (m *Mock) checkPlanStage(st driver.UsagePlanStage) error {
	ad, err := m.getAPI(st.RestAPIID)
	if err != nil {
		return err
	}

	ad.mu.RLock()
	defer ad.mu.RUnlock()

	if _, ok := ad.stages[st.Stage]; !ok {
		return cerrors.Newf(cerrors.NotFound, "Invalid stage identifier specified %s", st.Stage)
	}

	return nil
}

// GetUsagePlan returns one usage plan.
func (m *Mock) GetUsagePlan(_ context.Context, id string) (*driver.UsagePlan, error) {
	m.regionMu.RLock()
	defer m.regionMu.RUnlock()

	plan, ok := m.plans[id]
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, msgUsagePlanNotFound)
	}

	out := copyPlan(plan)

	return &out, nil
}

// GetUsagePlans lists usage plans (those containing keyID when it is set).
func (m *Mock) GetUsagePlans(_ context.Context, keyID string, page driver.PageInput) (*driver.UsagePlanPage, error) {
	m.regionMu.RLock()

	all := make([]driver.UsagePlan, 0, len(m.plans))

	for id, p := range m.plans {
		if keyID != "" && !m.planKeys[id][keyID] {
			continue
		}

		all = append(all, copyPlan(p))
	}

	m.regionMu.RUnlock()

	sort.Slice(all, func(i, j int) bool {
		if all[i].Name != all[j].Name {
			return all[i].Name < all[j].Name
		}

		return all[i].ID < all[j].ID
	})

	items, next, err := pageOf(all, page)
	if err != nil {
		return nil, err
	}

	return &driver.UsagePlanPage{Items: items, Position: next}, nil
}

// UpdateUsagePlan applies a patchOperations document.
func (m *Mock) UpdateUsagePlan(_ context.Context, id string, ops []driver.PatchOperation) (*driver.UsagePlan, error) {
	m.regionMu.Lock()
	defer m.regionMu.Unlock()

	plan, ok := m.plans[id]
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, msgUsagePlanNotFound)
	}

	upd := copyPlan(plan)

	for _, op := range ops {
		if err := m.applyPlanPatch(&upd, op); err != nil {
			return nil, err
		}
	}

	if err := validateQuotaThrottle(upd.Quota, upd.Throttle); err != nil {
		return nil, err
	}

	*plan = upd

	out := copyPlan(plan)

	return &out, nil
}

func (m *Mock) applyPlanPatch(p *driver.UsagePlan, op driver.PatchOperation) error {
	switch {
	case op.Path == pathName:
		p.Name = op.Value
	case op.Path == pathDescription:
		p.Description = op.Value
	case op.Path == "/productCode":
		p.ProductCode = op.Value
	case strings.HasPrefix(op.Path, "/throttle/"), strings.HasPrefix(op.Path, "/quota/"):
		return applyLimitPatch(p, op)
	case op.Path == "/apiStages":
		return m.applyPlanStagePatch(p, op)
	case strings.HasPrefix(op.Path, "/apiStages/"):
		return applyStageThrottlePatch(p, op)
	default:
		return invalidPatchPath(op, pathName, pathDescription, "/productCode", "/throttle/burstLimit",
			"/throttle/rateLimit", pathQuotaLimit, "/quota/offset", "/quota/period", "/apiStages")
	}

	return nil
}

func applyLimitPatch(p *driver.UsagePlan, op driver.PatchOperation) error {
	if op.Op == opRemove && op.Path == "/throttle" {
		p.Throttle = nil

		return nil
	}

	switch op.Path {
	case "/throttle/burstLimit":
		n, err := strconv.Atoi(op.Value)
		if err != nil {
			return cerrors.New(cerrors.InvalidArgument, msgThrottle)
		}

		if p.Throttle == nil {
			p.Throttle = &driver.ThrottleSettings{}
		}

		p.Throttle.BurstLimit = n
	case "/throttle/rateLimit":
		f, err := strconv.ParseFloat(op.Value, 64)
		if err != nil {
			return cerrors.New(cerrors.InvalidArgument, msgThrottle)
		}

		if p.Throttle == nil {
			p.Throttle = &driver.ThrottleSettings{}
		}

		p.Throttle.RateLimit = f
	default:
		return applyQuotaPatch(p, op)
	}

	return nil
}

func applyQuotaPatch(p *driver.UsagePlan, op driver.PatchOperation) error {
	if p.Quota == nil {
		p.Quota = &driver.QuotaSettings{Period: driver.QuotaDay}
	}

	switch op.Path {
	case pathQuotaLimit, "/quota/offset":
		n, err := strconv.Atoi(op.Value)
		if err != nil {
			return cerrors.New(cerrors.InvalidArgument, msgQuota)
		}

		if op.Path == pathQuotaLimit {
			p.Quota.Limit = n
		} else {
			p.Quota.Offset = n
		}
	case "/quota/period":
		p.Quota.Period = op.Value
	default:
		return invalidPatchPath(op, pathQuotaLimit, "/quota/offset", "/quota/period")
	}

	return nil
}

func (m *Mock) applyPlanStagePatch(p *driver.UsagePlan, op driver.PatchOperation) error {
	apiID, stage, ok := strings.Cut(op.Value, ":")
	if !ok {
		return cerrors.New(cerrors.InvalidArgument, "apiStages value must be restApiId:stage")
	}

	switch op.Op {
	case opAdd:
		return m.addPlanStage(p, apiID, stage)
	case opRemove:
		kept := p.APIStages[:0]

		for _, s := range p.APIStages {
			if s.RestAPIID != apiID || s.Stage != stage {
				kept = append(kept, s)
			}
		}

		p.APIStages = kept
	}

	return nil
}

func (m *Mock) addPlanStage(p *driver.UsagePlan, apiID, stage string) error {
	if err := m.checkPlanStage(driver.UsagePlanStage{RestAPIID: apiID, Stage: stage}); err != nil {
		return err
	}

	for _, s := range p.APIStages {
		if s.RestAPIID == apiID && s.Stage == stage {
			return nil
		}
	}

	p.APIStages = append(p.APIStages, driver.UsagePlanStage{RestAPIID: apiID, Stage: stage})

	return nil
}

// applyStageThrottlePatch handles /apiStages/{apiId:stage}/throttle/{resourcePath}/{method}
// (remove) and .../{method}/rateLimit|burstLimit (replace), with ~1 escaping the
// slashes of the resource path.
func applyStageThrottlePatch(p *driver.UsagePlan, op driver.PatchOperation) error {
	const minTokens = 2

	head, tail, ok := strings.Cut(strings.TrimPrefix(op.Path, "/apiStages/"), "/throttle/")
	tokens := strings.Split(tail, "/")

	if !ok || len(tokens) < minTokens {
		return invalidPatchPath(op, "/apiStages/{apiId:stage}/throttle/{resourcePath}/{method}/{burstLimit|rateLimit}")
	}

	apiID, stage, _ := strings.Cut(unescapePointer(head), ":")
	key := unescapePointer(tokens[0]) + "/" + tokens[1]

	for i := range p.APIStages {
		s := &p.APIStages[i]
		if s.RestAPIID != apiID || s.Stage != stage {
			continue
		}

		if op.Op == opRemove {
			delete(s.Throttle, key)

			return nil
		}

		const withField = 3
		if len(tokens) != withField {
			return invalidPatchPath(op, "/apiStages/{apiId:stage}/throttle/{resourcePath}/{method}/{burstLimit|rateLimit}")
		}

		return setMethodThrottle(s, key, tokens[2], op.Value)
	}

	return cerrors.Newf(cerrors.NotFound, "Invalid stage identifier specified %s", stage)
}

func setMethodThrottle(s *driver.UsagePlanStage, key, field, value string) error {
	if s.Throttle == nil {
		s.Throttle = map[string]driver.ThrottleSettings{}
	}

	cur := s.Throttle[key]

	switch field {
	case "burstLimit":
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return cerrors.New(cerrors.InvalidArgument, msgThrottle)
		}

		cur.BurstLimit = n
	case "rateLimit":
		f, err := strconv.ParseFloat(value, 64)
		if err != nil || f < 0 {
			return cerrors.New(cerrors.InvalidArgument, msgThrottle)
		}

		cur.RateLimit = f
	default:
		return cerrors.New(cerrors.InvalidArgument, msgThrottle)
	}

	s.Throttle[key] = cur

	return nil
}

// DeleteUsagePlan removes a plan and its key attachments.
func (m *Mock) DeleteUsagePlan(_ context.Context, id string) error {
	m.regionMu.Lock()
	defer m.regionMu.Unlock()

	if _, ok := m.plans[id]; !ok {
		return cerrors.New(cerrors.NotFound, msgUsagePlanNotFound)
	}

	delete(m.plans, id)

	m.usageMu.Lock()
	defer m.usageMu.Unlock()

	for key := range m.planKeys[id] {
		delete(m.usage, id+"|"+key)
		delete(m.buckets, id+"|"+key)
	}

	delete(m.planKeys, id)

	return nil
}

// CreateUsagePlanKey attaches an existing API key to a plan.
func (m *Mock) CreateUsagePlanKey(_ context.Context, planID, keyID, keyType string) (*driver.UsagePlanKey, error) {
	if keyType != keyTypeAPIKey {
		return nil, cerrors.New(cerrors.InvalidArgument, msgKeyType)
	}

	m.regionMu.Lock()
	defer m.regionMu.Unlock()

	if _, ok := m.plans[planID]; !ok {
		return nil, cerrors.New(cerrors.NotFound, msgUsagePlanNotFound)
	}

	key, ok := m.keys[keyID]
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, msgNoSuchKey)
	}

	if m.planKeys[planID][keyID] {
		return nil, cerrors.New(cerrors.AlreadyExists, msgUsagePlanKeyExists)
	}

	m.planKeys[planID][keyID] = true

	return &driver.UsagePlanKey{ID: keyID, Type: keyTypeAPIKey, Value: key.Value, Name: key.Name}, nil
}

// GetUsagePlanKey returns one attached key.
func (m *Mock) GetUsagePlanKey(_ context.Context, planID, keyID string) (*driver.UsagePlanKey, error) {
	m.regionMu.RLock()
	defer m.regionMu.RUnlock()

	if _, ok := m.plans[planID]; !ok {
		return nil, cerrors.New(cerrors.NotFound, msgUsagePlanNotFound)
	}

	key, ok := m.keys[keyID]
	if !ok || !m.planKeys[planID][keyID] {
		return nil, cerrors.New(cerrors.NotFound, msgUsagePlanKeyNotFound)
	}

	return &driver.UsagePlanKey{ID: keyID, Type: keyTypeAPIKey, Value: key.Value, Name: key.Name}, nil
}

// GetUsagePlanKeys lists a plan's keys ordered by name then id.
func (m *Mock) GetUsagePlanKeys(
	_ context.Context, planID, nameQuery string, page driver.PageInput,
) (*driver.UsagePlanKeyPage, error) {
	m.regionMu.RLock()

	if _, ok := m.plans[planID]; !ok {
		m.regionMu.RUnlock()

		return nil, cerrors.New(cerrors.NotFound, msgUsagePlanNotFound)
	}

	all := make([]driver.UsagePlanKey, 0, len(m.planKeys[planID]))

	for id := range m.planKeys[planID] {
		k := m.keys[id]
		if k == nil || (nameQuery != "" && !strings.HasPrefix(k.Name, nameQuery)) {
			continue
		}

		all = append(all, driver.UsagePlanKey{ID: id, Type: keyTypeAPIKey, Value: k.Value, Name: k.Name})
	}

	m.regionMu.RUnlock()

	sort.Slice(all, func(i, j int) bool {
		if all[i].Name != all[j].Name {
			return all[i].Name < all[j].Name
		}

		return all[i].ID < all[j].ID
	})

	items, next, err := pageOf(all, page)
	if err != nil {
		return nil, err
	}

	return &driver.UsagePlanKeyPage{Items: items, Position: next}, nil
}

// DeleteUsagePlanKey detaches a key from a plan.
func (m *Mock) DeleteUsagePlanKey(_ context.Context, planID, keyID string) error {
	m.regionMu.Lock()
	defer m.regionMu.Unlock()

	if _, ok := m.plans[planID]; !ok {
		return cerrors.New(cerrors.NotFound, msgUsagePlanNotFound)
	}

	if !m.planKeys[planID][keyID] {
		return cerrors.New(cerrors.NotFound, msgUsagePlanKeyNotFound)
	}

	delete(m.planKeys[planID], keyID)

	m.usageMu.Lock()
	delete(m.usage, planID+"|"+keyID)
	delete(m.buckets, planID+"|"+keyID)
	m.usageMu.Unlock()

	return nil
}

func (m *Mock) GetUsage(_ context.Context, in *driver.GetUsageInput) (*driver.Usage, error) {
	start, end, err := parseUsageRange(in.StartDate, in.EndDate)
	if err != nil {
		return nil, err
	}

	m.regionMu.RLock()
	defer m.regionMu.RUnlock()

	plan, ok := m.plans[in.UsagePlanID]
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, msgUsagePlanNotFound)
	}

	ids, err := m.usageKeyIDs(in)
	if err != nil {
		return nil, err
	}

	page, next, err := pageOf(ids, in.PageInput)
	if err != nil {
		return nil, err
	}

	out := &driver.Usage{UsagePlanID: plan.ID, StartDate: in.StartDate, EndDate: in.EndDate, Position: next}

	m.usageMu.Lock()
	defer m.usageMu.Unlock()

	for _, id := range page {
		out.Items = append(out.Items, m.keyUsage(plan, id, start, end))
	}

	return out, nil
}

// parseUsageRange parses and orders the startDate and endDate of GetUsage.
func parseUsageRange(startDate, endDate string) (start, end time.Time, err error) {
	start, err = time.Parse(time.DateOnly, startDate)
	if err != nil {
		return start, end, cerrors.New(cerrors.InvalidArgument, "startDate must be YYYY-MM-DD")
	}

	end, err = time.Parse(time.DateOnly, endDate)
	if err != nil || end.Before(start) {
		return start, end, cerrors.New(cerrors.InvalidArgument, "endDate must be YYYY-MM-DD and not before startDate")
	}

	return start, end, nil
}

// usageKeyIDs lists the plan's key ids GetUsage reports, sorted: every key, or
// the one named by KeyID (which must be attached).
func (m *Mock) usageKeyIDs(in *driver.GetUsageInput) ([]string, error) {
	ids := make([]string, 0, len(m.planKeys[in.UsagePlanID]))

	for id := range m.planKeys[in.UsagePlanID] {
		if in.KeyID == "" || in.KeyID == id {
			ids = append(ids, id)
		}
	}

	if in.KeyID != "" && len(ids) == 0 {
		return nil, cerrors.New(cerrors.NotFound, msgUsagePlanKeyNotFound)
	}

	sort.Strings(ids)

	return ids, nil
}

// keyUsage builds one key's per-day {used, remaining} list. Remaining is -1 when
// the plan has no quota.
func (m *Mock) keyUsage(plan *driver.UsagePlan, keyID string, start, end time.Time) driver.UsageItem {
	item := driver.UsageItem{KeyID: keyID}
	days := m.usage[plan.ID+"|"+keyID]

	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		used := days[d.Format(time.DateOnly)]
		remaining := int64(-1)

		if plan.Quota != nil {
			remaining = int64(plan.Quota.Limit) - periodUsed(plan, days, d)
			if remaining < 0 {
				remaining = 0
			}
		}

		item.Days = append(item.Days, [2]int64{used, remaining})
	}

	return item
}

// periodUsed sums the requests counted in the quota period containing day, up to
// and including day, plus the plan's offset.
func periodUsed(plan *driver.UsagePlan, days map[string]int64, day time.Time) int64 {
	first := periodStart(plan.Quota.Period, day)

	var used int64

	for d := first; !d.After(day); d = d.AddDate(0, 0, 1) {
		used += days[d.Format(time.DateOnly)]
	}

	return used + int64(plan.Quota.Offset)
}

// periodStart is the first day of the quota period containing day.
func periodStart(period string, day time.Time) time.Time {
	switch period {
	case driver.QuotaWeek:
		back := (int(day.Weekday()) + daysInWeek - int(time.Monday)) % daysInWeek

		return day.AddDate(0, 0, -back)
	case driver.QuotaMonth:
		return time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, time.UTC)
	default:
		return day
	}
}

func copyThrottle(t *driver.ThrottleSettings) *driver.ThrottleSettings {
	if t == nil {
		return nil
	}

	v := *t

	return &v
}

func copyQuota(q *driver.QuotaSettings) *driver.QuotaSettings {
	if q == nil {
		return nil
	}

	v := *q

	return &v
}

func copyPlanStages(in []driver.UsagePlanStage) []driver.UsagePlanStage {
	if in == nil {
		return nil
	}

	out := make([]driver.UsagePlanStage, len(in))

	for i, s := range in {
		out[i] = driver.UsagePlanStage{RestAPIID: s.RestAPIID, Stage: s.Stage}

		if s.Throttle != nil {
			out[i].Throttle = make(map[string]driver.ThrottleSettings, len(s.Throttle))
			for k, v := range s.Throttle {
				out[i].Throttle[k] = v
			}
		}
	}

	return out
}

func copyPlan(p *driver.UsagePlan) driver.UsagePlan {
	out := *p
	out.APIStages = copyPlanStages(p.APIStages)
	out.Throttle = copyThrottle(p.Throttle)
	out.Quota = copyQuota(p.Quota)
	out.Tags = copyStrMap(p.Tags)

	return out
}
