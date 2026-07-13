package telemetry

import "encoding/json"

// MarshalJSON keeps a valid empty page as entries:[] instead of entries:null.
func (p MACPage) MarshalJSON() ([]byte, error) {
	type alias MACPage
	value := alias(p)
	if value.Entries == nil {
		value.Entries = make([]MACEntry, 0)
	}
	return json.Marshal(value)
}

// MarshalJSON keeps a valid empty page as entries:[] instead of entries:null.
func (p ARPPage) MarshalJSON() ([]byte, error) {
	type alias ARPPage
	value := alias(p)
	if value.Entries == nil {
		value.Entries = make([]ARPEntry, 0)
	}
	return json.Marshal(value)
}
