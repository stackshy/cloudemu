package ssm

import (
	"embed"
	"fmt"
	"path"
	"strings"
	"time"

	ssmdriver "github.com/stackshy/cloudemu/v2/providers/aws/ssm/driver"
)

// catalogFS holds the content of the AWS-owned documents, one file per
// document named after it.
//
//go:embed catalog/*.json
var catalogFS embed.FS

// catalogCreated is the CreatedDate the AWS-owned documents report. The real
// ones carry the date AWS published them, which callers never depend on.
func catalogCreated() time.Time {
	return time.Date(2017, time.January, 1, 0, 0, 0, 0, time.UTC)
}

// catalogTypes maps each AWS-owned document to its DocumentType.
func catalogTypes() map[string]string {
	return map[string]string{
		"AWS-RunShellScript":             ssmdriver.DocumentTypeCommand,
		"AWS-RunPowerShellScript":        ssmdriver.DocumentTypeCommand,
		"AWS-UpdateSSMAgent":             ssmdriver.DocumentTypeCommand,
		"AWS-ConfigureAWSPackage":        ssmdriver.DocumentTypeCommand,
		"AWS-RunPatchBaseline":           ssmdriver.DocumentTypeCommand,
		"AWS-RunRemoteScript":            ssmdriver.DocumentTypeCommand,
		"AWS-ApplyAnsiblePlaybooks":      ssmdriver.DocumentTypeCommand,
		"AWS-ConfigureDocker":            ssmdriver.DocumentTypeCommand,
		"AmazonCloudWatch-ManageAgent":   ssmdriver.DocumentTypeCommand,
		"AWS-StartSSHSession":            ssmdriver.DocumentTypeSession,
		"AWS-StartPortForwardingSession": ssmdriver.DocumentTypeSession,
		"AWS-StopEC2Instance":            ssmdriver.DocumentTypeAutomation,
		"AWS-StartEC2Instance":           ssmdriver.DocumentTypeAutomation,
		"AWS-RestartEC2Instance":         ssmdriver.DocumentTypeAutomation,
	}
}

// loadCatalog parses the embedded AWS-owned documents. A document that fails
// to parse is a build defect, so it panics.
func loadCatalog() map[string]*document {
	types := catalogTypes()
	out := make(map[string]*document, len(types))

	for name, docType := range types {
		raw, err := catalogFS.ReadFile(path.Join("catalog", name+".json"))
		if err != nil {
			panic(fmt.Sprintf("ssm: catalog document %s: %v", name, err))
		}

		content := strings.TrimSpace(string(raw))

		meta, err := parseContent(content, ssmdriver.FormatJSON, docType)
		if err != nil {
			panic(fmt.Sprintf("ssm: catalog document %s: %v", name, err))
		}

		out[name] = &document{
			name: name, docType: docType, owner: ssmdriver.OwnerAmazon, created: catalogCreated(),
			versions: map[int]*docVersion{1: {
				number: 1, content: content, format: ssmdriver.FormatJSON, created: catalogCreated(), meta: meta,
			}},
			defaultVersion: 1, latestVersion: 1, nextVersion: 2,
		}
	}

	return out
}
