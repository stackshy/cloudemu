package savingsplans

import (
	"strings"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

func TestCreateRejectsOutOfRangeCommitment(t *testing.T) {
	s, _ := newTestStore(t)

	bad := []string{"0", "0.000", "-1", "-0.5", "0.0009", "1000000.01", "NaN", "Inf", "1.123456", "abc", ""}
	for _, c := range bad {
		t.Run(c, func(t *testing.T) {
			_, err := s.create(&createInput{savingsPlanOfferingID: storeTestOffering, commitment: c})
			if !cerrors.IsInvalidArgument(err) {
				t.Fatalf("commitment %q: err = %v, want InvalidArgument", c, err)
			}
		})
	}

	good := []string{"0.001", "1", "1.500", "0.12345", "1000000"}
	for _, c := range good {
		t.Run("ok_"+c, func(t *testing.T) {
			if _, err := s.create(&createInput{savingsPlanOfferingID: storeTestOffering, commitment: c}); err != nil {
				t.Fatalf("commitment %q: unexpected error %v", c, err)
			}
		})
	}
}

func TestTagsRejectInvalidKeys(t *testing.T) {
	s, _ := newTestStore(t)

	_, err := s.create(&createInput{
		savingsPlanOfferingID: storeTestOffering,
		commitment:            "1",
		tags:                  map[string]string{"": "v"},
	})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("create with empty tag key: err = %v, want InvalidArgument", err)
	}

	if _, err := s.create(&createInput{savingsPlanOfferingID: storeTestOffering, commitment: "1"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	arn := s.describe(planFilter{})[0].ARN

	cases := map[string]map[string]string{
		"empty key":  {"": "v"},
		"long key":   {strings.Repeat("k", maxTagKeyLength+1): "v"},
		"long value": {"k": strings.Repeat("v", maxTagValueLength+1)},
	}
	for name, tags := range cases {
		if err := s.tag(arn, tags); !cerrors.IsInvalidArgument(err) {
			t.Fatalf("%s: err = %v, want InvalidArgument", name, err)
		}
	}

	if tags, _ := s.tagsOf(arn); len(tags) != 0 {
		t.Fatalf("rejected tags must not be stored: %v", tags)
	}

	if err := s.tag(arn, map[string]string{"k": ""}); err != nil {
		t.Fatalf("empty tag value is valid: %v", err)
	}
}

func TestPaginate(t *testing.T) {
	items := []int{1, 2, 3, 4, 5}

	page, next, err := paginate(items, 2, "")
	if err != nil || len(page) != 2 || page[0] != 1 || next == "" {
		t.Fatalf("page1 = %v next=%q err=%v", page, next, err)
	}

	page, next, err = paginate(items, 2, next)
	if err != nil || len(page) != 2 || page[0] != 3 || next == "" {
		t.Fatalf("page2 = %v next=%q err=%v", page, next, err)
	}

	page, next, err = paginate(items, 2, next)
	if err != nil || len(page) != 1 || page[0] != 5 || next != "" {
		t.Fatalf("page3 = %v next=%q err=%v", page, next, err)
	}

	if page, next, err = paginate(items, 0, ""); err != nil || len(page) != 5 || next != "" {
		t.Fatalf("unbounded = %v next=%q err=%v", page, next, err)
	}

	for _, n := range []int{-1, maxDescribePageResults + 1} {
		if _, _, err := paginate(items, n, ""); !cerrors.IsInvalidArgument(err) {
			t.Fatalf("maxResults %d: err = %v, want InvalidArgument", n, err)
		}
	}

	if _, _, err := paginate(items, 1, "!!not-a-token"); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("bad token: err = %v, want InvalidArgument", err)
	}
}

func TestDescribeOfferingsFilters(t *testing.T) {
	s, _ := newTestStore(t)

	ids := func(f *offeringFilter) []string {
		var out []string
		for _, o := range s.describeOfferings(f) {
			out = append(out, o.id)
		}

		return out
	}

	cases := []struct {
		name string
		f    *offeringFilter
		want []string
	}{
		{"productType SageMaker", &offeringFilter{productType: "SageMaker"}, []string{"sp-offering-sagemaker-1yr-no"}},
		{"productType Fargate", &offeringFilter{productType: "Fargate"},
			[]string{"sp-offering-compute-1yr-no", "sp-offering-compute-3yr-all"}},
		{"productType RDS", &offeringFilter{productType: "RDS"}, nil},
		{"instanceFamily m5", &offeringFilter{attrs: []wireFilter{{Name: "instanceFamily", Values: []string{"m5"}}}},
			[]string{"sp-offering-ec2-1yr-partial"}},
		{"instanceFamily c5", &offeringFilter{attrs: []wireFilter{{Name: "instanceFamily", Values: []string{"c5"}}}}, nil},
		{"region match", &offeringFilter{attrs: []wireFilter{{Name: "region", Values: []string{storeTestRegion}}}},
			[]string{"sp-offering-ec2-1yr-partial"}},
		{"region miss", &offeringFilter{attrs: []wireFilter{{Name: "region", Values: []string{"eu-west-1"}}}}, nil},
		{"paymentOptions", &offeringFilter{paymentOptions: toSet([]string{paymentAllUpfront})},
			[]string{"sp-offering-compute-3yr-all"}},
		{"durations", &offeringFilter{durations: toSet(int64sToStrings([]int64{termThreeYear}))},
			[]string{"sp-offering-compute-3yr-all"}},
		{"currencies EUR", &offeringFilter{currencies: toSet([]string{"EUR"})}, nil},
		{"serviceCodes", &offeringFilter{serviceCodes: toSet([]string{"AmazonSageMaker"})},
			[]string{"sp-offering-sagemaker-1yr-no"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ids(tc.f)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
