package bedrockagent

import (
	"net/http"

	badriver "github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// promptVersionsSegments is the segment count of /prompts/{id}/versions.
const promptVersionsSegments = 2

type createPromptVersionRequest struct {
	Description string            `json:"description"`
	ClientToken string            `json:"clientToken"`
	Tags        map[string]string `json:"tags"`
}

func createPromptVersion(w http.ResponseWriter, r *http.Request, c badriver.PromptVersions, promptID string) {
	var in createPromptVersionRequest
	if !decodeBody(w, r, &in) {
		return
	}

	prompt, err := c.CreatePromptVersion(r.Context(), promptID, in.Description, in.Tags)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSONStatus(w, http.StatusCreated, toPromptJSON(prompt))
}

// servePromptVersion answers GetPrompt and DeletePrompt calls that name a
// promptVersion.
func servePromptVersion(w http.ResponseWriter, r *http.Request, c badriver.PromptVersions, promptID, version string) {
	if r.Method == http.MethodGet {
		prompt, err := c.GetPromptVersion(r.Context(), promptID, version)
		if err != nil {
			writeErr(w, err)

			return
		}

		writeJSON(w, toPromptJSON(prompt))

		return
	}

	if err := c.DeletePromptVersion(r.Context(), promptID, version); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, deletePromptResponse{ID: resourceIDOf(promptID, "prompt/"), Version: version})
}
