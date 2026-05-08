package apisix

import (
	"encoding/json"
)

// UnmarshalEtcdValue supports both raw resource payloads (stored in etcd) and
// the common Admin API response-like wrapper: {"key": "...", "value": {...}}.
func UnmarshalEtcdValue[T any](data []byte, out *T) error {
	var wrapper struct {
		Value json.RawMessage `json:"value"`
	}

	if err := json.Unmarshal(data, &wrapper); err == nil && len(wrapper.Value) > 0 {
		return json.Unmarshal(wrapper.Value, out)
	}
	return json.Unmarshal(data, out)
}

