package llm

// ModelPricing is USD cost per 1M tokens, matching how providers publish
// their price sheets.
type ModelPricing struct {
	InputPer1M  float64
	OutputPer1M float64
}

// PriceTable computes cost locally, since OpenAI-compatible APIs return
// token usage but never a price (docs/DESIGN_REVIEW.md F-8).
type PriceTable struct {
	Prices          map[string]ModelPricing
	DisplayCurrency string  // e.g. "USD", "JPY"
	USDToDisplay    float64 // multiply a USD amount by this to get DisplayCurrency
}

// Cost returns (usd, display, ok). ok is false when the model has no
// configured pricing, in which case cost is reported as zero rather than
// guessed.
func (t PriceTable) Cost(model string, usage Usage) (usd, display float64, ok bool) {
	price, found := t.Prices[model]
	if !found {
		return 0, 0, false
	}
	usd = float64(usage.InputTokens)/1_000_000*price.InputPer1M +
		float64(usage.OutputTokens)/1_000_000*price.OutputPer1M
	rate := t.USDToDisplay
	if rate == 0 {
		rate = 1
	}
	return usd, usd * rate, true
}
