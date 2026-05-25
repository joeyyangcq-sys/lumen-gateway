package bootstrap

import (
	"errors"
	"os"
	"testing"
)

func TestBootstrapLoadAndValidate(t *testing.T) {
	// 1. Load non-existent file
	_, err := Load("non-existent-bootstrap.yaml")
	if err == nil {
		t.Error("expected error for non-existent file")
	}

	// 2. Load invalid YAML
	tmpFile, err := os.CreateTemp("", "bootstrap-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	_, _ = tmpFile.WriteString("gateway: [invalid")
	_ = tmpFile.Close()

	_, err = Load(tmpFile.Name())
	if err == nil {
		t.Error("expected error for invalid YAML")
	}

	// 3. Load valid YAML (defaults applied)
	tmpFile2, err := os.CreateTemp("", "bootstrap-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile2.Name())
	_, _ = tmpFile2.WriteString(`
gateway:
  source: file
file:
  path: configs/lumen.yaml
`)
	_ = tmpFile2.Close()

	opts, err := Load(tmpFile2.Name())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if opts.Gateway.Listen != ":18080" {
		t.Errorf("expected default listen :18080, got %q", opts.Gateway.Listen)
	}

	// 4. Validate errors
	t.Run("empty listen", func(t *testing.T) {
		o := Options{}
		o.Gateway.Listen = ""
		if err := o.Validate(); err == nil || err.Error() != "gateway.listen cannot be empty" {
			t.Errorf("expected gateway.listen cannot be empty, got %v", err)
		}
	})

	t.Run("empty file path when source=file", func(t *testing.T) {
		o := Options{
			Gateway: GatewayOptions{Listen: ":8080", Source: "file"},
		}
		if err := o.Validate(); err == nil || err.Error() != "file.path cannot be empty when gateway.source=file" {
			t.Errorf("expected file.path cannot be empty, got %v", err)
		}
	})

	t.Run("empty etcd endpoints when source=etcd_apisix", func(t *testing.T) {
		o := Options{
			Gateway: GatewayOptions{Listen: ":8080", Source: "etcd_apisix"},
		}
		if err := o.Validate(); err == nil || err.Error() != "etcd.endpoints cannot be empty when gateway.source=etcd_apisix" {
			t.Errorf("expected etcd.endpoints cannot be empty, got %v", err)
		}
	})

	t.Run("empty etcd prefix when source=etcd_apisix", func(t *testing.T) {
		o := Options{
			Gateway: GatewayOptions{Listen: ":8080", Source: "etcd_apisix"},
			Etcd:    EtcdOptions{Endpoints: []string{"localhost:2379"}},
		}
		if err := o.Validate(); err == nil || err.Error() != "etcd.prefix cannot be empty when gateway.source=etcd_apisix" {
			t.Errorf("expected etcd.prefix cannot be empty, got %v", err)
		}
	})

	t.Run("unsupported gateway source", func(t *testing.T) {
		o := Options{
			Gateway: GatewayOptions{Listen: ":8080", Source: "invalid"},
		}
		if err := o.Validate(); err == nil || !errors.Is(err, err) {
			t.Errorf("expected unsupported source error, got %v", err)
		}
	})
}
