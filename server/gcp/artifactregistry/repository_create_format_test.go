package artifactregistry_test

import (
	"errors"
	"net/http"
	"testing"

	ar "google.golang.org/api/artifactregistry/v1"
	"google.golang.org/api/googleapi"
)

// TestCreateRepositoryFormatRequired guards the real-user divergence that a
// create with a missing, FORMAT_UNSPECIFIED, or unknown format must fail with
// INVALID_ARGUMENT (HTTP 400) — real Artifact Registry requires a concrete,
// valid format, which is also immutable. The handler previously defaulted a
// missing format to DOCKER and stored an unknown format verbatim, silently
// masking the client error and diverging from GCP.
func TestCreateRepositoryFormatRequired(t *testing.T) {
	svc, _ := newARService(t)

	cases := []struct {
		name string
		repo *ar.Repository
	}{
		{"missing", &ar.Repository{}},
		{"unspecified", &ar.Repository{Format: "FORMAT_UNSPECIFIED"}},
		{"unknown", &ar.Repository{Format: "NOT_A_FORMAT"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Projects.Locations.Repositories.
				Create(testParent, tc.repo).RepositoryId("repo-" + tc.name).Do()
			if err == nil {
				t.Fatalf("create with %s format: got nil error, want INVALID_ARGUMENT", tc.name)
			}

			var gerr *googleapi.Error
			if !errors.As(err, &gerr) {
				t.Fatalf("create with %s format: error %v is not a googleapi.Error", tc.name, err)
			}

			if gerr.Code != http.StatusBadRequest {
				t.Fatalf("create with %s format: code=%d want %d", tc.name, gerr.Code, http.StatusBadRequest)
			}
		})
	}
}

// TestCreateRepositoryFormatAccepted confirms the validation does not reject a
// valid format (the common real-user path); a DOCKER create still succeeds and
// round-trips its format on get.
func TestCreateRepositoryFormatAccepted(t *testing.T) {
	svc, _ := newARService(t)

	op, err := svc.Projects.Locations.Repositories.
		Create(testParent, &ar.Repository{Format: "DOCKER"}).RepositoryId("ok").Do()
	if err != nil {
		t.Fatalf("create with DOCKER format: %v", err)
	}

	if !op.Done {
		t.Fatalf("create operation not done")
	}

	got, err := svc.Projects.Locations.Repositories.Get(testParent + "/repositories/ok").Do()
	if err != nil {
		t.Fatalf("get after create: %v", err)
	}

	if got.Format != "DOCKER" {
		t.Fatalf("format=%q want DOCKER", got.Format)
	}
}
