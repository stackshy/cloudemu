package savingsplans

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
)

// describePage drives DescribeSavingsPlans through the handler and returns the
// page's plan ids and nextToken.
func describePage(t *testing.T, h *Handler, body string) (ids []string, next string) {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/DescribeSavingsPlans", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("DescribeSavingsPlans %s: status %d: %s", body, rec.Code, rec.Body.String())
	}

	var out struct {
		SavingsPlans []struct {
			ID string `json:"savingsPlanId"`
		} `json:"savingsPlans"`
		NextToken string `json:"nextToken"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}

	for _, p := range out.SavingsPlans {
		ids = append(ids, p.ID)
	}

	return ids, out.NextToken
}

func putPlan(h *Handler, id string, now time.Time) {
	h.store.plans.Set(id, &savingsPlan{
		ID: id, ARN: h.store.planARN(id), OfferingID: storeTestOffering, Commitment: "1",
		Currency: currencyUSD, Region: storeTestRegion, TermSeconds: termOneYear,
		Start: now, End: now.Add(time.Hour), State: stateActive,
	})
}

// TestDescribeSavingsPlansPagesStayStableAcrossInserts is the regression test
// for offset tokens over the id-sorted plan list: a plan created between page
// calls that sorts before the cursor must not shift the next page (repeating an
// already-returned plan).
func TestDescribeSavingsPlansPagesStayStableAcrossInserts(t *testing.T) {
	clock := config.NewFakeClock(time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC))
	h := New(storeTestAccount, storeTestRegion, clock)

	for _, id := range []string{"b", "d", "f"} {
		putPlan(h, id, clock.Now())
	}

	first, next := describePage(t, h, `{"maxResults":2}`)
	if strings.Join(first, ",") != "b,d" || next == "" {
		t.Fatalf("page1 = %v next=%q", first, next)
	}

	// A plan that sorts before the cursor arrives between the two calls.
	putPlan(h, "a", clock.Now())

	second, last := describePage(t, h, `{"maxResults":2,"nextToken":"`+next+`"}`)
	if strings.Join(second, ",") != "f" || last != "" {
		t.Fatalf("page2 = %v next=%q, want [f] and no token", second, last)
	}
}
