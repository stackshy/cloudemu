package scope

import "testing"

func TestMatches(t *testing.T) {
	rgA := Scope{Subscription: "sub1", ResourceGroup: "rg-a"}
	rgB := Scope{Subscription: "sub1", ResourceGroup: "rg-b"}

	if !rgA.Matches(Scope{}) {
		t.Error("zero filter must match everything")
	}
	if !rgA.Matches(Scope{Subscription: "sub1"}) {
		t.Error("subscription filter must span its resource groups")
	}
	if rgA.Matches(rgB) {
		t.Error("different resource groups must not match")
	}
	if !(Scope{}).Matches(rgA) {
		t.Error("unscoped resources stay visible under scoped filters")
	}
	if rgA.Matches(Scope{Subscription: "other"}) {
		t.Error("different subscriptions must not match")
	}
}
