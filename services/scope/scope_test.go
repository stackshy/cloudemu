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

func TestInResourceGroup(t *testing.T) {
	tests := []struct {
		name    string
		s       Scope
		sub, rg string
		want    bool
	}{
		{"exact", Scope{Subscription: "sub1", ResourceGroup: "rg1"}, "sub1", "rg1", true},
		{"rg case-insensitive", Scope{Subscription: "sub1", ResourceGroup: "Cas1"}, "sub1", "cas1", true},
		{"sub case-insensitive", Scope{Subscription: "SUB1", ResourceGroup: "rg1"}, "sub1", "rg1", true},
		{"rg prefix is not a match", Scope{Subscription: "sub1", ResourceGroup: "rg10"}, "sub1", "rg1", false},
		{"other rg", Scope{Subscription: "sub1", ResourceGroup: "rg2"}, "sub1", "rg1", false},
		{"other sub", Scope{Subscription: "sub2", ResourceGroup: "rg1"}, "sub1", "rg1", false},
		{"zero scope never matches", Scope{}, "sub1", "rg1", false},
		{"zero scope with empty rg", Scope{}, "", "", false},
		{"unrecorded sub matches", Scope{ResourceGroup: "rg1"}, "sub1", "rg1", true},
		{"empty caller sub matches", Scope{Subscription: "sub1", ResourceGroup: "rg1"}, "", "rg1", true},
		{"project scope", Scope{Project: "p1"}, "sub1", "rg1", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.s.InResourceGroup(tc.sub, tc.rg); got != tc.want {
				t.Errorf("InResourceGroup(%q, %q) = %v, want %v", tc.sub, tc.rg, got, tc.want)
			}
		})
	}
}
