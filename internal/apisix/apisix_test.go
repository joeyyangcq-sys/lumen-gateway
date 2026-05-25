package apisix

import (
	"testing"
)

func TestIDUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    ID
		wantErr bool
	}{
		{"null", "null", "", false},
		{"empty", "", "", false},
		{"string id", `"123"`, "123", false},
		{"number integer id", `456`, "456", false},
		{"number float id", `78.9`, "78.9", false},
		{"invalid json", `{`, "", true},
		{"invalid string", `"123`, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var id ID
			err := id.UnmarshalJSON([]byte(tt.input))
			if (err != nil) != tt.wantErr {
				t.Fatalf("UnmarshalJSON() error = %v, wantErr %t", err, tt.wantErr)
			}
			if !tt.wantErr && id != tt.want {
				t.Errorf("got %q, want %q", id, tt.want)
			}
		})
	}

	// Test String() method
	id := ID("hello")
	if id.String() != "hello" {
		t.Errorf("expected string 'hello', got %q", id.String())
	}
}

func TestUnmarshalEtcdValue(t *testing.T) {
	type resource struct {
		Name string `json:"name"`
	}

	// Test plain payload
	var r1 resource
	err := UnmarshalEtcdValue([]byte(`{"name":"test1"}`), &r1)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Name != "test1" {
		t.Errorf("expected test1, got %q", r1.Name)
	}

	// Test wrapped payload
	var r2 resource
	err = UnmarshalEtcdValue([]byte(`{"key":"/apisix/routes/1","value":{"name":"test2"}}`), &r2)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Name != "test2" {
		t.Errorf("expected test2, got %q", r2.Name)
	}
}

func TestNewSnapshot(t *testing.T) {
	s := NewSnapshot()
	if s.Routes == nil || s.Services == nil || s.Upstreams == nil || s.PluginConfig == nil || s.GlobalRules == nil {
		t.Error("NewSnapshot should initialize all maps")
	}
}
