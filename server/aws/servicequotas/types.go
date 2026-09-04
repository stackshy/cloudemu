package servicequotas

// Wire request shapes. Only the fields CloudEmu reads are modeled; the SDK
// sends others (e.g. pagination tokens) that decode harmlessly into nothing.

type getServiceQuotaInput struct {
	ServiceCode string `json:"ServiceCode"`
	QuotaCode   string `json:"QuotaCode"`
}

type listServiceQuotasInput struct {
	ServiceCode string `json:"ServiceCode"`
}

type requestIncreaseInput struct {
	ServiceCode  string  `json:"ServiceCode"`
	QuotaCode    string  `json:"QuotaCode"`
	DesiredValue float64 `json:"DesiredValue"`
}

type listHistoryInput struct {
	ServiceCode string `json:"ServiceCode"`
	QuotaCode   string `json:"QuotaCode"`
}

// serviceQuota is the wire shape of a Service Quotas ServiceQuota.
type serviceQuota struct {
	ServiceCode string  `json:"ServiceCode"`
	ServiceName string  `json:"ServiceName"`
	QuotaArn    string  `json:"QuotaArn"`
	QuotaCode   string  `json:"QuotaCode"`
	QuotaName   string  `json:"QuotaName"`
	Value       float64 `json:"Value"`
	Unit        string  `json:"Unit"`
	Adjustable  bool    `json:"Adjustable"`
	GlobalQuota bool    `json:"GlobalQuota"`
}

// requestedQuota is the wire shape of a RequestedServiceQuotaChange. Created and
// LastUpdated are epoch seconds, the AWS JSON protocol timestamp encoding.
type requestedQuota struct {
	ID           string  `json:"Id"`
	ServiceCode  string  `json:"ServiceCode"`
	ServiceName  string  `json:"ServiceName"`
	QuotaArn     string  `json:"QuotaArn"`
	QuotaCode    string  `json:"QuotaCode"`
	QuotaName    string  `json:"QuotaName"`
	DesiredValue float64 `json:"DesiredValue"`
	Status       string  `json:"Status"`
	Unit         string  `json:"Unit"`
	GlobalQuota  bool    `json:"GlobalQuota"`
	Created      float64 `json:"Created"`
	LastUpdated  float64 `json:"LastUpdated"`
}

type getServiceQuotaOutput struct {
	Quota serviceQuota `json:"Quota"`
}

type getDefaultOutput struct {
	Quota serviceQuota `json:"Quota"`
}

type listServiceQuotasOutput struct {
	Quotas []serviceQuota `json:"Quotas"`
}

type requestIncreaseOutput struct {
	RequestedQuota requestedQuota `json:"RequestedQuota"`
}

type listHistoryOutput struct {
	RequestedQuotas []requestedQuota `json:"RequestedQuotas"`
}
