package contract

// EventReceipt identifies the stored event and reports how many jobs it created.
type EventReceipt struct {
	EventID      string `json:"event_id"`
	Event        string `json:"event"`
	Deliveries   int    `json:"deliveries"`
	Deduplicated bool   `json:"deduplicated"`
}
