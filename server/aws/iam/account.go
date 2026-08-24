package iam

import (
	"context"
	"encoding/xml"
	"net/http"
	"sort"
	"strconv"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// accountSummarizer is the AWS-specific GetAccountSummary surface.
type accountSummarizer interface {
	AccountSummary(ctx context.Context) (map[string]int, error)
}

// passwordPolicyManager is the AWS-specific account-password-policy surface.
type passwordPolicyManager interface {
	UpdateAccountPasswordPolicy(ctx context.Context, p iamdriver.PasswordPolicy) error
	GetAccountPasswordPolicy(ctx context.Context) (*iamdriver.PasswordPolicy, error)
	DeleteAccountPasswordPolicy(ctx context.Context) error
}

type summaryEntryXML struct {
	Key   string `xml:"key"`
	Value int    `xml:"value"`
}

type summaryMapXML struct {
	Entry []summaryEntryXML `xml:"entry"`
}

type getAccountSummaryResponse struct {
	XMLName  xml.Name                `xml:"GetAccountSummaryResponse"`
	Xmlns    string                  `xml:"xmlns,attr"`
	Result   getAccountSummaryResult `xml:"GetAccountSummaryResult"`
	Metadata responseMetadata        `xml:"ResponseMetadata"`
}

type getAccountSummaryResult struct {
	SummaryMap summaryMapXML `xml:"SummaryMap"`
}

type passwordPolicyXML struct {
	MinimumPasswordLength      int  `xml:"MinimumPasswordLength"`
	RequireSymbols             bool `xml:"RequireSymbols"`
	RequireNumbers             bool `xml:"RequireNumbers"`
	RequireUppercaseCharacters bool `xml:"RequireUppercaseCharacters"`
	RequireLowercaseCharacters bool `xml:"RequireLowercaseCharacters"`
	AllowUsersToChangePassword bool `xml:"AllowUsersToChangePassword"`
	ExpirePasswords            bool `xml:"ExpirePasswords"`
	MaxPasswordAge             int  `xml:"MaxPasswordAge,omitempty"`
	PasswordReusePrevention    int  `xml:"PasswordReusePrevention,omitempty"`
	HardExpiry                 bool `xml:"HardExpiry"`
}

type getAccountPasswordPolicyResponse struct {
	XMLName  xml.Name                       `xml:"GetAccountPasswordPolicyResponse"`
	Xmlns    string                         `xml:"xmlns,attr"`
	Result   getAccountPasswordPolicyResult `xml:"GetAccountPasswordPolicyResult"`
	Metadata responseMetadata               `xml:"ResponseMetadata"`
}

type getAccountPasswordPolicyResult struct {
	PasswordPolicy passwordPolicyXML `xml:"PasswordPolicy"`
}

type updateAccountPasswordPolicyResponse struct {
	XMLName  xml.Name         `xml:"UpdateAccountPasswordPolicyResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type deleteAccountPasswordPolicyResponse struct {
	XMLName  xml.Name         `xml:"DeleteAccountPasswordPolicyResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

func (h *Handler) getAccountSummary(w http.ResponseWriter, r *http.Request) {
	summarizer, ok := h.iam.(accountSummarizer)
	if !ok {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidAction", "GetAccountSummary not supported")
		return
	}

	summary, err := summarizer.AccountSummary(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	keys := make([]string, 0, len(summary))
	for k := range summary {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	entries := make([]summaryEntryXML, 0, len(keys))
	for _, k := range keys {
		entries = append(entries, summaryEntryXML{Key: k, Value: summary[k]})
	}

	awsquery.WriteXMLResponse(w, getAccountSummaryResponse{
		Xmlns:    Namespace,
		Result:   getAccountSummaryResult{SummaryMap: summaryMapXML{Entry: entries}},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) getAccountPasswordPolicy(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.iam.(passwordPolicyManager)
	if !ok {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidAction", "password policy not supported")
		return
	}

	p, err := mgr.GetAccountPasswordPolicy(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, getAccountPasswordPolicyResponse{
		Xmlns:    Namespace,
		Result:   getAccountPasswordPolicyResult{PasswordPolicy: toPasswordPolicyXML(p)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) updateAccountPasswordPolicy(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.iam.(passwordPolicyManager)
	if !ok {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidAction", "password policy not supported")
		return
	}

	if err := mgr.UpdateAccountPasswordPolicy(r.Context(), passwordPolicyFromForm(r)); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, updateAccountPasswordPolicyResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deleteAccountPasswordPolicy(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.iam.(passwordPolicyManager)
	if !ok {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidAction", "password policy not supported")
		return
	}

	if err := mgr.DeleteAccountPasswordPolicy(r.Context()); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteAccountPasswordPolicyResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// toPasswordPolicyXML maps a stored policy to its wire shape, deriving the
// read-only ExpirePasswords flag from MaxPasswordAge.
func toPasswordPolicyXML(p *iamdriver.PasswordPolicy) passwordPolicyXML {
	return passwordPolicyXML{
		MinimumPasswordLength:      p.MinimumPasswordLength,
		RequireSymbols:             p.RequireSymbols,
		RequireNumbers:             p.RequireNumbers,
		RequireUppercaseCharacters: p.RequireUppercaseCharacters,
		RequireLowercaseCharacters: p.RequireLowercaseCharacters,
		AllowUsersToChangePassword: p.AllowUsersToChangePassword,
		ExpirePasswords:            p.MaxPasswordAge > 0,
		MaxPasswordAge:             p.MaxPasswordAge,
		PasswordReusePrevention:    p.PasswordReusePrevention,
		HardExpiry:                 p.HardExpiry,
	}
}

// passwordPolicyFromForm parses the UpdateAccountPasswordPolicy request. Omitted
// boolean parameters default to false, matching real IAM.
func passwordPolicyFromForm(r *http.Request) iamdriver.PasswordPolicy {
	return iamdriver.PasswordPolicy{
		MinimumPasswordLength:      formInt(r, "MinimumPasswordLength"),
		RequireSymbols:             formBool(r, "RequireSymbols"),
		RequireNumbers:             formBool(r, "RequireNumbers"),
		RequireUppercaseCharacters: formBool(r, "RequireUppercaseCharacters"),
		RequireLowercaseCharacters: formBool(r, "RequireLowercaseCharacters"),
		AllowUsersToChangePassword: formBool(r, "AllowUsersToChangePassword"),
		MaxPasswordAge:             formInt(r, "MaxPasswordAge"),
		PasswordReusePrevention:    formInt(r, "PasswordReusePrevention"),
		HardExpiry:                 formBool(r, "HardExpiry"),
	}
}

func formInt(r *http.Request, key string) int {
	n, err := strconv.Atoi(r.Form.Get(key))
	if err != nil {
		return 0
	}

	return n
}

func formBool(r *http.Request, key string) bool {
	return r.Form.Get(key) == formValueTrue
}
