package types

// VideoQuoteRequest describes one complete work, repeated WorksCount times.
// Parameters use the existing OpenAI video JSON contract; they never start work.
type VideoQuoteRequest struct {
	Model      string                     `json:"model"`
	WorksCount int                        `json:"works_count"`
	Segments   []VideoQuoteSegmentRequest `json:"segments"`
}

type VideoQuoteSegmentRequest struct {
	ID              string                    `json:"id"`
	Parameters      map[string]any            `json:"parameters"`
	ReferenceCounts VideoQuoteReferenceCounts `json:"reference_counts"`
}

type VideoQuoteReferenceCounts struct {
	Images int `json:"images"`
	Videos int `json:"videos"`
	Audios int `json:"audios"`
}

type VideoQuoteSegment struct {
	ID              string                    `json:"id"`
	Parameters      map[string]any            `json:"parameters"`
	Usage           map[string]any            `json:"usage"`
	Quota           int                       `json:"quota"`
	EstimatedAmount string                    `json:"estimated_amount"`
	ReferenceCounts VideoQuoteReferenceCounts `json:"reference_counts"`
}

type VideoQuoteResponse struct {
	Object           string              `json:"object"`
	SchemaVersion    int                 `json:"schema_version"`
	QuoteID          string              `json:"quote_id"`
	AccountBinding   string              `json:"account_binding"`
	Model            string              `json:"model"`
	WorksCount       int                 `json:"works_count"`
	Segments         []VideoQuoteSegment `json:"segments"`
	OneWorkQuota     int                 `json:"one_work_quota"`
	TotalQuota       int                 `json:"total_quota"`
	Currency         string              `json:"currency"`
	QuotaPerUSD      float64             `json:"quota_per_usd"`
	USDExchangeRate  float64             `json:"usd_exchange_rate"`
	EstimatedOneWork string              `json:"estimated_one_work"`
	EstimatedTotal   string              `json:"estimated_total"`
	PricingRevision  string              `json:"pricing_revision"`
	CreatedAt        int64               `json:"created_at"`
	ExpiresAt        int64               `json:"expires_at"`
	EstimateOnly     bool                `json:"estimate_only"`
	Charged          bool                `json:"charged"`
	Submitted        bool                `json:"submitted"`
	Reserved         bool                `json:"reserved"`
}

type VideoQuoteError struct {
	Code       string
	Message    string
	StatusCode int
}

func (e *VideoQuoteError) Error() string { return e.Message }
