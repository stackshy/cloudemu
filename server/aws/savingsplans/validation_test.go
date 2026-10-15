package savingsplans

import (
	"strings"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

func TestCreateRejectsOutOfRangeCommitment(t *testing.T) {
	s, _ := newTestStore(t)

	bad := []string{
		"0", "0.000", "-1", "-0.5", "0.0009", "1000000.01", "NaN", "Inf", "1.123456", "abc", "",
		"1e2", "0x1p3", "+5", "1.23456e2", " 1", "1.", ".5",
	}
	for _, c := range bad {
		t.Run(c, func(t *testing.T) {
			_, err := s.create(&createInput{savingsPlanOfferingID: storeTestOffering, commitment: c})
			if !cerrors.IsInvalidArgument(err) {
				t.Fatalf("commitment %q: err = %v, want InvalidArgument", c, err)
			}
		})
	}

	good := []string{"0.001", "1", "1.500", "0.12345", "1000000", "123.456"}
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
		"aws: key":   {"aws:team": "v"},
		"AWS: key":   {"AWS:team": "v"},
		"Aws: key":   {"Aws:Team": "v"},
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
	items := []string{"a", "b", "c", "d", "e"}
	key := func(s string) string { return s }
	pr := func(n int, token string) pageRequest {
		return pageRequest{maxResults: n, minResults: 1, set: true, nextToken: token}
	}

	page, next, err := paginate(items, key, pr(2, ""))
	if err != nil || strings.Join(page, "") != "ab" || next == "" {
		t.Fatalf("page1 = %v next=%q err=%v", page, next, err)
	}

	page, next, err = paginate(items, key, pr(2, next))
	if err != nil || strings.Join(page, "") != "cd" || next == "" {
		t.Fatalf("page2 = %v next=%q err=%v", page, next, err)
	}

	page, next, err = paginate(items, key, pr(2, next))
	if err != nil || strings.Join(page, "") != "e" || next != "" {
		t.Fatalf("page3 = %v next=%q err=%v", page, next, err)
	}

	// Omitted maxResults returns everything.
	if page, next, err = paginate(items, key, pageRequest{minResults: 1}); err != nil || len(page) != 5 || next != "" {
		t.Fatalf("omitted = %v next=%q err=%v", page, next, err)
	}

	// 0 is valid where the operation's minimum is 0 (offerings) and means all.
	if page, _, err = paginate(items, key, pageRequest{set: true}); err != nil || len(page) != 5 {
		t.Fatalf("maxResults 0 with min 0 = %v err=%v", page, err)
	}

	for _, n := range []int{-1, 0, maxDescribePageResults + 1} {
		if _, _, err := paginate(items, key, pr(n, "")); !cerrors.IsInvalidArgument(err) {
			t.Fatalf("maxResults %d (min 1): err = %v, want InvalidArgument", n, err)
		}
	}

	// "Mg==" is valid base64 ("2") but not a token this handler issued.
	for _, tok := range []string{"!!not-a-token", "Mg=="} {
		if _, _, err := paginate(items, key, pr(1, tok)); !cerrors.IsInvalidArgument(err) {
			t.Fatalf("bad token %q: err = %v, want InvalidArgument", tok, err)
		}
	}
}

func TestDescribeOfferingsRejectsUnknownFilterNameStore(t *testing.T) {
	s, _ := newTestStore(t)

	for _, name := range []string{"instancefamily", "Region", "bogus", ""} {
		_, err := s.describeOfferings(&offeringFilter{attrs: []wireFilter{{Name: name, Values: []string{"x"}}}})
		if !cerrors.IsInvalidArgument(err) {
			t.Fatalf("filter name %q: err = %v, want InvalidArgument", name, err)
		}
	}
}

func TestDescribeOfferingsFilters(t *testing.T) {
	s, _ := newTestStore(t)

	ids := func(f *offeringFilter) []string {
		matched, err := s.describeOfferings(f)
		if err != nil {
			t.Fatalf("describeOfferings: %v", err)
		}

		var out []string
		for _, o := range matched {
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
