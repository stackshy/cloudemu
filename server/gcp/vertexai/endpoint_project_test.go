package vertexai_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEndpointCreateUsesPathProject: an endpoint created under a project other
// than the server's own is named, stored and read back under the request
// path's project. Terraform reads the resource at that path right after
// create; a name under the server project made it vanish ("root object was
// present, but now absent").
func TestEndpointCreateUsesPathProject(t *testing.T) {
	url := newServer(t)

	for name, project := range map[string]string{
		"other project":  "tf-project",
		"server project": "mock-project",
	} {
		t.Run(name, func(t *testing.T) {
			parent := "projects/" + project + "/locations/us-central1"
			want := parent + "/endpoints/77" + project[:1]

			op := do(t, http.MethodPost, url+"/v1/"+parent+"/endpoints?endpointId=77"+project[:1],
				map[string]any{"displayName": "ep"})
			assert.True(t, strings.HasPrefix(op["name"].(string), parent+"/operations/"), op["name"])
			assert.Equal(t, want, op["response"].(map[string]any)["name"])

			got := do(t, http.MethodGet, url+"/v1/"+want, nil)
			assert.Equal(t, want, got["name"])
			assert.Equal(t, "ep", got["displayName"])

			do(t, http.MethodPatch, url+"/v1/"+want+"?updateMask=displayName", map[string]any{"displayName": "ep2"})
			assert.Equal(t, "ep2", do(t, http.MethodGet, url+"/v1/"+want, nil)["displayName"])

			do(t, http.MethodDelete, url+"/v1/"+want, nil)

			req, err := http.NewRequest(http.MethodGet, url+"/v1/"+want, http.NoBody)
			require.NoError(t, err)

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)

			_ = resp.Body.Close()

			assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		})
	}
}
