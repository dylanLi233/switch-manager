package telemetry

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

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

// UnmarshalJSON distinguishes a missing age_seconds field from the valid value
// zero and rejects unknown fields inside a MAC record.
func (e *MACEntry) UnmarshalJSON(data []byte) error {
	var wire struct {
		MACAddress    *string       `json:"mac_address"`
		VLANID        *int          `json:"vlan_id"`
		InterfaceName *string       `json:"interface_name"`
		EntryType     *MACEntryType `json:"entry_type"`
		AgeSeconds    *int64        `json:"age_seconds"`
	}
	if err := decodeCompleteJSON(data, &wire); err != nil {
		return err
	}
	if wire.MACAddress == nil || wire.VLANID == nil || wire.InterfaceName == nil || wire.EntryType == nil || wire.AgeSeconds == nil {
		return errors.New("MAC entry is missing a required field")
	}
	normalized, err := NormalizeMACEntry(MACEntry{MACAddress: *wire.MACAddress, VLANID: *wire.VLANID, InterfaceName: *wire.InterfaceName, EntryType: *wire.EntryType, AgeSeconds: *wire.AgeSeconds})
	if err != nil {
		return err
	}
	*e = normalized
	return nil
}

// UnmarshalJSON requires every non-optional ARP field and rejects unknown
// fields. mac_address remains optional only for INCOMPLETE entries.
func (e *ARPEntry) UnmarshalJSON(data []byte) error {
	var wire struct {
		IPAddress     *string   `json:"ip_address"`
		MACAddress    *string   `json:"mac_address"`
		InterfaceName *string   `json:"interface_name"`
		State         *ARPState `json:"state"`
		AgeSeconds    *int64    `json:"age_seconds"`
	}
	if err := decodeCompleteJSON(data, &wire); err != nil {
		return err
	}
	if wire.IPAddress == nil || wire.InterfaceName == nil || wire.State == nil || wire.AgeSeconds == nil {
		return errors.New("ARP entry is missing a required field")
	}
	mac := ""
	if wire.MACAddress != nil {
		mac = *wire.MACAddress
	}
	normalized, err := NormalizeARPEntry(ARPEntry{IPAddress: *wire.IPAddress, MACAddress: mac, InterfaceName: *wire.InterfaceName, State: *wire.State, AgeSeconds: *wire.AgeSeconds})
	if err != nil {
		return err
	}
	*e = normalized
	return nil
}

func decodeCompleteJSON(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON contains a trailing value")
		}
		return err
	}
	return nil
}
