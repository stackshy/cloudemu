package cloudbilling

import "encoding/json"

// billingAccount mirrors cloudbilling.BillingAccount. Name has the form
// "billingAccounts/{id}".
type billingAccount struct {
	Name                 string `json:"name"`
	Open                 bool   `json:"open"`
	DisplayName          string `json:"displayName,omitempty"`
	MasterBillingAccount string `json:"masterBillingAccount,omitempty"`
	Parent               string `json:"parent,omitempty"`
	CurrencyCode         string `json:"currencyCode,omitempty"`
}

// listBillingAccountsResponse is the billingAccounts.list body.
type listBillingAccountsResponse struct {
	BillingAccounts []*billingAccount `json:"billingAccounts,omitempty"`
	NextPageToken   string            `json:"nextPageToken,omitempty"`
}

// projectBillingInfo mirrors cloudbilling.ProjectBillingInfo. Name has the form
// "projects/{projectId}/billingInfo".
type projectBillingInfo struct {
	Name               string `json:"name"`
	ProjectID          string `json:"projectId"`
	BillingAccountName string `json:"billingAccountName,omitempty"`
	BillingEnabled     bool   `json:"billingEnabled"`
}

// listProjectBillingInfoResponse is the billingAccounts.projects.list body.
type listProjectBillingInfoResponse struct {
	ProjectBillingInfo []*projectBillingInfo `json:"projectBillingInfo,omitempty"`
	NextPageToken      string                `json:"nextPageToken,omitempty"`
}

// service mirrors cloudbilling.Service. Name has the form
// "services/{serviceId}".
type service struct {
	Name               string `json:"name"`
	ServiceID          string `json:"serviceId"`
	DisplayName        string `json:"displayName,omitempty"`
	BusinessEntityName string `json:"businessEntityName,omitempty"`
}

// listServicesResponse is the services.list body.
type listServicesResponse struct {
	Services      []*service `json:"services,omitempty"`
	NextPageToken string     `json:"nextPageToken,omitempty"`
}

// money mirrors google.type.Money. Units is JSON-encoded as a string, matching
// the Cloud Billing wire format the SDK expects.
type money struct {
	CurrencyCode string `json:"currencyCode,omitempty"`
	Units        int64  `json:"units,omitempty,string"`
	Nanos        int64  `json:"nanos,omitempty"`
}

// pricingExpression is the tiered-rate portion of a SKU's pricing info.
type pricingExpression struct {
	UsageUnit            string       `json:"usageUnit,omitempty"`
	UsageUnitDescription string       `json:"usageUnitDescription,omitempty"`
	BaseUnit             string       `json:"baseUnit,omitempty"`
	DisplayQuantity      float64      `json:"displayQuantity,omitempty"`
	TieredRates          []tieredRate `json:"tieredRates,omitempty"`
}

// tieredRate is one price tier within a pricingExpression.
type tieredRate struct {
	StartUsageAmount float64 `json:"startUsageAmount,omitempty"`
	UnitPrice        *money  `json:"unitPrice,omitempty"`
}

// pricingInfo mirrors cloudbilling.PricingInfo (the subset the emulator seeds).
type pricingInfo struct {
	Summary                string             `json:"summary,omitempty"`
	CurrencyConversionRate float64            `json:"currencyConversionRate,omitempty"`
	PricingExpression      *pricingExpression `json:"pricingExpression,omitempty"`
}

// sku mirrors cloudbilling.Sku. Name has the form
// "services/{serviceId}/skus/{skuId}".
type sku struct {
	Name                string        `json:"name"`
	SkuID               string        `json:"skuId"`
	Description         string        `json:"description,omitempty"`
	ServiceProviderName string        `json:"serviceProviderName,omitempty"`
	ServiceRegions      []string      `json:"serviceRegions,omitempty"`
	PricingInfo         []pricingInfo `json:"pricingInfo,omitempty"`
}

// listSkusResponse is the services.skus.list body.
type listSkusResponse struct {
	Skus          []*sku `json:"skus,omitempty"`
	NextPageToken string `json:"nextPageToken,omitempty"`
}

// budget mirrors billingbudgets.GoogleCloudBillingBudgetsV1Budget. Name has the
// form "billingAccounts/{id}/budgets/{budgetId}". The deeply-nested optional
// budgetFilter and notificationsRule sub-objects are carried verbatim as raw
// JSON so any field the client sets round-trips exactly.
type budget struct {
	Name              string          `json:"name"`
	DisplayName       string          `json:"displayName,omitempty"`
	Amount            *budgetAmount   `json:"amount,omitempty"`
	BudgetFilter      json.RawMessage `json:"budgetFilter,omitempty"`
	ThresholdRules    []thresholdRule `json:"thresholdRules,omitempty"`
	NotificationsRule json.RawMessage `json:"notificationsRule,omitempty"`
	OwnershipScope    string          `json:"ownershipScope,omitempty"`
	Etag              string          `json:"etag,omitempty"`
}

// budgetAmount mirrors GoogleCloudBillingBudgetsV1BudgetAmount.
type budgetAmount struct {
	SpecifiedAmount  *money          `json:"specifiedAmount,omitempty"`
	LastPeriodAmount json.RawMessage `json:"lastPeriodAmount,omitempty"`
}

// thresholdRule mirrors GoogleCloudBillingBudgetsV1ThresholdRule.
type thresholdRule struct {
	ThresholdPercent float64 `json:"thresholdPercent,omitempty"`
	SpendBasis       string  `json:"spendBasis,omitempty"`
}

// listBudgetsResponse is the budgets.list body.
type listBudgetsResponse struct {
	Budgets       []*budget `json:"budgets,omitempty"`
	NextPageToken string    `json:"nextPageToken,omitempty"`
}
