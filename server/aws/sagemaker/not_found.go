package sagemaker

import (
	"net/http"
	"regexp"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
)

// validationNotFoundOps are the operations whose SDK output does not model a
// ResourceNotFound error. SageMaker reports a missing target of these as a
// ValidationException ("Could not find endpoint ..."), which is what the SDK
// waiters (EndpointDeleted, NotebookInstanceDeleted) and Terraform's
// not-found detection key off. Operations that DO model ResourceNotFound keep
// returning it.
var validationNotFoundOps = map[string]bool{ //nolint:gochecknoglobals // static lookup table
	"DescribeEndpoint": true, "DescribeEndpointConfig": true, "DescribeModel": true,
	"DeleteEndpoint": true, "DeleteEndpointConfig": true, "DeleteModel": true,
	"UpdateEndpoint": true, "UpdateEndpointWeightsAndCapacities": true,
	"UpdateNotebookInstance": true, "UpdateNotebookInstanceLifecycleConfig": true,
	"DescribeNotebookInstance": true, "DeleteNotebookInstance": true,
	"StartNotebookInstance": true, "StopNotebookInstance": true,
	"DescribeNotebookInstanceLifecycleConfig": true, "DeleteNotebookInstanceLifecycleConfig": true,
	"DescribeCodeRepository": true, "DeleteCodeRepository": true,
	"DescribeProject": true, "DeleteProject": true, "UpdateProject": true,
	"DescribeWorkteam": true, "UpdateWorkteam": true, "DescribeSubscribedWorkteam": true,
	"DescribeWorkforce": true, "UpdateWorkforce": true, "DeleteWorkforce": true,
	"DescribeInferenceComponent": true, "DeleteInferenceComponent": true,
	"DescribeModelPackage": true, "DeleteModelPackage": true, "UpdateModelPackage": true,
	"PutModelPackageGroupPolicy": true, "GetModelPackageGroupPolicy": true, "DeleteModelPackageGroupPolicy": true,
	"DescribeModelPackageGroup": true, "DeleteModelPackageGroup": true,
	"AddTags": true, "DeleteTags": true, "ListTags": true,
}

// notFoundAsValidation marks a response whose operation reports a missing
// target as a ValidationException.
type notFoundAsValidation struct{ http.ResponseWriter }

var notFoundMsg = regexp.MustCompile(`^(.+?) ("[^"]*") not found$`)

// notFoundValidation converts a NotFound driver error into the
// "Could not find <kind> <name>." ValidationException message.
func notFoundValidation(err error) string {
	msg := cerrors.Message(err)
	if m := notFoundMsg.FindStringSubmatch(msg); m != nil {
		// DescribeNotebookInstance reports a missing instance as the bare
		// "RecordNotFound" message, which clients such as Terraform match on.
		switch m[1] {
		case "notebook instance":
			return "RecordNotFound"
		case "project":
			// Terraform's project and workforce readers match these phrasings.
			return "Project " + m[2] + " does not exist."
		case "workforce":
			return "No workforce found with name " + m[2] + "."
		}

		return "Could not find " + m[1] + " " + m[2] + "."
	}

	return msg
}

func writeNotFoundAsValidation(w http.ResponseWriter, err error) {
	wire.WriteJSONError(w, http.StatusBadRequest, "ValidationException", notFoundValidation(err))
}
