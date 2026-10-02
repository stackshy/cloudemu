package ssm

import (
	"regexp"
	"slices"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
)

// maxTargetTypeLength is the TargetType length limit.
const maxTargetTypeLength = 200

var (
	targetTypePattern    = regexp.MustCompile(`^/[\w.\-:/]*$`)
	versionNumberPattern = regexp.MustCompile(`^[1-9]\d*$`)
)

// documentTypes is the botocore DocumentType enum, in model order.
func documentTypes() []string {
	return []string{
		"Command", "Policy", "Automation", "Session", "Package", "ApplicationConfiguration",
		"ApplicationConfigurationSchema", "DeploymentStrategy", "ChangeCalendar", "Automation.ChangeTemplate",
		"ProblemAnalysis", "ProblemAnalysisTemplate", "CloudFormation", "ConformancePackTemplate",
		"QuickSetup", "ManualApprovalPolicy", "AutoApprovalPolicy",
	}
}

// documentFormats is the botocore DocumentFormat enum.
func documentFormats() []string {
	return []string{"YAML", "JSON", "TEXT"}
}

// validEnum rejects a non-empty value outside allowed with the request
// validation error real SSM returns before the operation runs.
func validEnum(field, value string, allowed []string) error {
	if value == "" || slices.Contains(allowed, value) {
		return nil
	}

	return ssmErrf(excValidation, errors.InvalidArgument,
		"1 validation error detected: Value '%s' at '%s' failed to satisfy constraint: "+
			"Member must satisfy enum value set: [%s]", value, field, strings.Join(allowed, ", "))
}

// validTargetType checks the TargetType pattern and length.
func validTargetType(t string) error {
	if t == "" {
		return nil
	}

	if len(t) > maxTargetTypeLength {
		return ssmErrf(excValidation, errors.InvalidArgument,
			"1 validation error detected: Value '%s' at 'targetType' failed to satisfy constraint: "+
				"Member must have length less than or equal to %d", t, maxTargetTypeLength)
	}

	if !targetTypePattern.MatchString(t) {
		return ssmErrf(excValidation, errors.InvalidArgument,
			"1 validation error detected: Value '%s' at 'targetType' failed to satisfy constraint: "+
				"Member must satisfy regular expression pattern: ^\\/[\\w\\.\\-\\:\\/]*$", t)
	}

	return nil
}

// validDocumentInput checks the enum and pattern fields CreateDocument and
// UpdateDocument share. docType is empty for UpdateDocument.
func validDocumentInput(docType, format, targetType, versionName string) error {
	if err := validVersionName(versionName); err != nil {
		return err
	}

	if err := validEnum("documentType", docType, documentTypes()); err != nil {
		return err
	}

	if err := validEnum("documentFormat", format, documentFormats()); err != nil {
		return err
	}

	return validTargetType(targetType)
}

// validVersionNumber checks a DocumentVersionNumber, which unlike
// DocumentVersion does not accept $LATEST or $DEFAULT.
func validVersionNumber(v string) error {
	if versionNumberPattern.MatchString(v) {
		return nil
	}

	return ssmErrf(excValidation, errors.InvalidArgument,
		"1 validation error detected: Value '%s' at 'documentVersion' failed to satisfy constraint: "+
			"Member must satisfy regular expression pattern: ^[1-9][0-9]*$", v)
}

// awsOwnedName reports whether a name sits in the namespace AWS reserves for
// its own documents. SendCommand accepts such a name even when the emulator
// holds no content for it, as the real service has hundreds of them.
func awsOwnedName(name string) bool {
	return strings.HasPrefix(name, "AWS") || strings.HasPrefix(name, "Amazon")
}
