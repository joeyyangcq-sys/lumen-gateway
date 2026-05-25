package app

import (
	"os"
	"testing"
)

func TestAppRunTestConfig(t *testing.T) {
	// Create a temporary valid yaml file
	tmpFile, err := os.CreateTemp("", "valid-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	_, _ = tmpFile.WriteString(`
servers:
  main:
    listen: :8080
routes:
  user-api:
    paths: [/api/users]
    service: user-service
services:
  user-service:
    upstream: user-upstream
upstreams:
  user-upstream:
    endpoints:
      - address: 127.0.0.1:9001
`)
	_ = tmpFile.Close()

	// Test -test flag with valid config
	args := []string{"lumen-gateway", "-config", tmpFile.Name(), "-test"}
	err = Run(args)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	// Test -test flag with non-existent config
	args = []string{"lumen-gateway", "-config", "non-existent-config.yaml", "-test"}
	err = Run(args)
	if err == nil {
		t.Error("expected error for non-existent config")
	}

	// Test invalid flags
	args = []string{"lumen-gateway", "-invalid-flag"}
	err = Run(args)
	if err == nil {
		t.Error("expected error for invalid flag")
	}
}

func TestAppRunGatewayNewError(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "invalid-compile-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	_, _ = tmpFile.WriteString(`
servers:
  main1:
    listen: :8080
  main2:
    listen: :8081
routes:
  user-api:
    paths: [/api/users]
    service: user-service
services:
  user-service:
    upstream: user-upstream
upstreams:
  user-upstream:
    endpoints:
      - address: 127.0.0.1:9001
`)
	_ = tmpFile.Close()

	// 运行，由于有 2 个 server，gateway.New 会报错
	args := []string{"lumen-gateway", "-config", tmpFile.Name()}
	err = Run(args)
	if err == nil {
		t.Error("expected error from gateway.New due to multiple servers")
	}
}

