package apigateway

import (
	"context"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

const (
	headerAPIKey = "x-api-key"
	hoursPerDay  = 24
)

// enforce runs the method request checks in API Gateway's order: authorization,
// API key and usage plan, stage throttling, then request validation. It returns
// the error response of the first check that fails, or nil.
func (m *Mock) enforce(ctx context.Context, req *driver.ProxyRequest, route *resolvedRoute, reqID string) *driver.ProxyResponse {
	if resp := m.authorize(ctx, req, route, reqID); resp != nil {
		return resp
	}

	if route.method.APIKeyRequired {
		if resp := m.checkAPIKey(req, route, reqID); resp != nil {
			return resp
		}
	}

	if resp := m.checkStageThrottle(req, route, reqID); resp != nil {
		return resp
	}

	return m.validateRequest(req, route, reqID)
}

// requestKey reads the API key the caller presented: the x-api-key header, or
// the authorizer's usageIdentifierKey when the API's key source is AUTHORIZER.
func requestKey(req *driver.ProxyRequest, route *resolvedRoute) string {
	if route.apiKeySource == "AUTHORIZER" {
		return route.usageKey
	}

	return headerValue(req.Headers, headerAPIKey)
}

// checkAPIKey requires an enabled key that belongs to a usage plan covering this
// stage, then applies that plan's throttle and quota.
func (m *Mock) checkAPIKey(req *driver.ProxyRequest, route *resolvedRoute, reqID string) *driver.ProxyResponse {
	value := requestKey(req, route)
	if value == "" {
		return m.gatewayResponse(route, req, reqID, respInvalidKey, "Forbidden")
	}

	m.regionMu.RLock()
	defer m.regionMu.RUnlock()

	key := m.keyByValue(value)
	if key == nil || !key.Enabled {
		return m.gatewayResponse(route, req, reqID, respInvalidKey, "Forbidden")
	}

	plan := m.planCovering(key.ID, route.apiID, route.stage.StageName)
	if plan == nil {
		return m.gatewayResponse(route, req, reqID, respInvalidKey, "Forbidden")
	}

	route.apiKeyID = key.ID

	return m.applyPlanLimits(req, route, reqID, plan, key.ID)
}

func (m *Mock) keyByValue(value string) *driver.APIKey {
	for _, k := range m.keys {
		if k.Value == value {
			return k
		}
	}

	return nil
}

// planCovering returns the usage plan (lowest id first) that holds keyID and
// lists the API stage.
func (m *Mock) planCovering(keyID, apiID, stage string) *driver.UsagePlan {
	var best *driver.UsagePlan

	for id, p := range m.plans {
		if !m.planKeys[id][keyID] || !planHasStage(p, apiID, stage) {
			continue
		}

		if best == nil || id < best.ID {
			best = p
		}
	}

	return best
}

func planHasStage(p *driver.UsagePlan, apiID, stage string) bool {
	for _, s := range p.APIStages {
		if s.RestAPIID == apiID && s.Stage == stage {
			return true
		}
	}

	return false
}

// applyPlanLimits enforces the plan's throttle (per key, with a per-method
// override) and then counts the request against the quota. regionMu is held.
func (m *Mock) applyPlanLimits(
	req *driver.ProxyRequest, route *resolvedRoute, reqID string, plan *driver.UsagePlan, keyID string,
) *driver.ProxyResponse {
	m.usageMu.Lock()
	defer m.usageMu.Unlock()

	scope := plan.ID + "|" + keyID
	now := m.opts.Clock.Now().UTC()

	if th, perMethod := planThrottle(plan, route); th != nil && (th.RateLimit > 0 || th.BurstLimit > 0) {
		// The plan's own throttle is one bucket per key; only a per-method
		// override is counted separately per resource path and method.
		bucketKey := scope
		if perMethod {
			bucketKey += "|" + route.resourcePath + "|" + route.method.HTTPMethod
		}

		b := m.bucket(bucketKey)
		if !b.take(now, th.RateLimit, th.BurstLimit) {
			return m.gatewayResponse(route, req, reqID, respThrottled, "Too Many Requests")
		}
	}

	if plan.Quota == nil {
		return nil
	}

	days := m.usage[scope]
	if days == nil {
		days = map[string]int64{}
		m.usage[scope] = days
	}

	today := now.Truncate(hoursPerDay * time.Hour)
	if periodUsed(plan, days, today) >= int64(plan.Quota.Limit) {
		return m.gatewayResponse(route, req, reqID, respQuotaExceeded, "Limit Exceeded")
	}

	days[today.Format(time.DateOnly)]++

	return nil
}

// planThrottle is the throttle in force for the method: the plan stage's
// per-method override (perMethod true), else the plan's own.
func planThrottle(plan *driver.UsagePlan, route *resolvedRoute) (th *driver.ThrottleSettings, perMethod bool) {
	for _, s := range plan.APIStages {
		if s.RestAPIID != route.apiID || s.Stage != route.stage.StageName {
			continue
		}

		if override, ok := s.Throttle[route.resourcePath+"/"+route.method.HTTPMethod]; ok {
			return &override, true
		}
	}

	return plan.Throttle, false
}

func (m *Mock) bucket(key string) *tokenBucket {
	b, ok := m.buckets[key]
	if !ok {
		b = &tokenBucket{}
		m.buckets[key] = b
	}

	return b
}

// checkStageThrottle applies the stage's method throttling settings.
func (m *Mock) checkStageThrottle(req *driver.ProxyRequest, route *resolvedRoute, reqID string) *driver.ProxyResponse {
	ms := methodSetting(&route.stage, route)
	if ms == nil || (ms.ThrottlingRateLimit <= 0 && ms.ThrottlingBurstLimit <= 0) {
		return nil
	}

	m.usageMu.Lock()
	defer m.usageMu.Unlock()

	b := m.bucket("stage|" + route.apiID + "|" + route.stage.StageName + "|" + route.resourcePath + "|" + route.method.HTTPMethod)
	if !b.take(m.opts.Clock.Now(), ms.ThrottlingRateLimit, ms.ThrottlingBurstLimit) {
		return m.gatewayResponse(route, req, reqID, respThrottled, "Too Many Requests")
	}

	return nil
}

// methodSetting returns the stage setting for the route's method, falling back to
// the "*/*" setting.
func methodSetting(st *driver.Stage, route *resolvedRoute) *driver.MethodSetting {
	if ms, ok := st.MethodSettings[methodSettingKey(route.resourcePath, route.method.HTTPMethod)]; ok {
		return ms
	}

	return st.MethodSettings["*/*"]
}

// splitList splits a comma-separated identity source.
func splitList(s string) []string {
	var out []string

	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}

	return out
}
