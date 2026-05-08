package apisix

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
)

// ID is an APISIX resource id that is often represented as either a JSON string or a JSON number.
// We normalize it to string to simplify map keys and cross-resource references.
type ID string

func (id ID) String() string {
	return string(id)
}

func (id *ID) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		*id = ""
		return nil
	}

	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*id = ID(s)
		return nil
	}

	var n json.Number
	if err := json.Unmarshal(data, &n); err != nil {
		return fmt.Errorf("apisix id: %w", err)
	}

	if i64, err := n.Int64(); err == nil {
		*id = ID(strconv.FormatInt(i64, 10))
		return nil
	}

	*id = ID(n.String())
	return nil
}

