package firestore

// Firestore REST JSON shapes (https://firestore.googleapis.com/v1/).

// document is the wire shape of a Firestore document.
type document struct {
	Name       string           `json:"name,omitempty"`
	Fields     map[string]value `json:"fields,omitempty"`
	CreateTime string           `json:"createTime,omitempty"`
	UpdateTime string           `json:"updateTime,omitempty"`
}

// value is a typed Firestore field value. Only one of the *Value fields is
// set per value object.
type value struct {
	NullValue      *string     `json:"nullValue,omitempty"`
	BooleanValue   *bool       `json:"booleanValue,omitempty"`
	IntegerValue   *string     `json:"integerValue,omitempty"`
	DoubleValue    *float64    `json:"doubleValue,omitempty"`
	TimestampValue *string     `json:"timestampValue,omitempty"`
	StringValue    *string     `json:"stringValue,omitempty"`
	BytesValue     *string     `json:"bytesValue,omitempty"`
	ReferenceValue *string     `json:"referenceValue,omitempty"`
	GeoPointValue  *geoPoint   `json:"geoPointValue,omitempty"`
	ArrayValue     *arrayValue `json:"arrayValue,omitempty"`
	MapValue       *mapValue   `json:"mapValue,omitempty"`
}

type geoPoint struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type arrayValue struct {
	Values []value `json:"values,omitempty"`
}

type mapValue struct {
	Fields map[string]value `json:"fields,omitempty"`
}

// listDocumentsResponse is the body for GET .../{collection}.
type listDocumentsResponse struct {
	Documents     []document `json:"documents,omitempty"`
	NextPageToken string     `json:"nextPageToken,omitempty"`
}

// errorEnvelope is the standard Google API error wrapper.
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    int              `json:"code"`
	Message string           `json:"message"`
	Status  string           `json:"status,omitempty"`
	Details []map[string]any `json:"details,omitempty"`
}
