package config

import (
	"testing"
	"time"
)

func TestGovernanceConfig_Validate_Disabled(t *testing.T) {
	cfg := &GovernanceConfig{
		Enabled: false,
	}

	err := cfg.Validate()
	if err != nil {
		t.Errorf("Validate should not fail when governance is disabled, got: %v", err)
	}
}

func TestGovernanceConfig_Validate_ValidConfig(t *testing.T) {
	cfg := &GovernanceConfig{
		Enabled:          true,
		ManagerURL:       "http://governance-manager:8080",
		ServiceName:      "diam-gw",
		PodIP:            "127.0.0.1",
		NotificationPort: 9002,
		Timeout:          10 * time.Second,
	}

	err := cfg.Validate()
	if err != nil {
		t.Errorf("Validate should not fail with valid config, got: %v", err)
	}
}

func TestGovernanceConfig_Validate_MissingManagerURL(t *testing.T) {
	cfg := &GovernanceConfig{
		Enabled:          true,
		ManagerURL:       "",
		ServiceName:      "diam-gw",
		PodIP:            "127.0.0.1",
		NotificationPort: 9002,
		Timeout:          10 * time.Second,
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("Validate should fail when manager_url is empty")
	}
	if err != nil && err.Error() != "governance.manager_url is required when governance is enabled" {
		t.Errorf("Expected manager_url error, got: %v", err)
	}
}

func TestGovernanceConfig_Validate_MissingServiceName(t *testing.T) {
	cfg := &GovernanceConfig{
		Enabled:          true,
		ManagerURL:       "http://governance-manager:8080",
		ServiceName:      "",
		PodIP:            "127.0.0.1",
		NotificationPort: 9002,
		Timeout:          10 * time.Second,
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("Validate should fail when service_name is empty")
	}
	if err != nil && err.Error() != "governance.service_name is required when governance is enabled" {
		t.Errorf("Expected service_name error, got: %v", err)
	}
}

func TestGovernanceConfig_Validate_InvalidPort(t *testing.T) {
	cfg := &GovernanceConfig{
		Enabled:          true,
		ManagerURL:       "http://governance-manager:8080",
		ServiceName:      "diam-gw",
		PodIP:            "127.0.0.1",
		NotificationPort: 99999,
		Timeout:          10 * time.Second,
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("Validate should fail when notification_port is invalid")
	}
}

func TestGovernanceConfig_Validate_MissingPodIP(t *testing.T) {
	cfg := &GovernanceConfig{
		Enabled:          true,
		ManagerURL:       "http://governance-manager:8080",
		ServiceName:      "diam-gw",
		PodIP:            "",
		NotificationPort: 9002,
		Timeout:          10 * time.Second,
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("Validate should fail when pod_ip is empty")
	}
	if err != nil && err.Error() != "governance.pod_ip is required when governance is enabled" {
		t.Errorf("Expected pod_ip error, got: %v", err)
	}
}

func TestGovernanceConfig_Validate_InvalidTimeout(t *testing.T) {
	cfg := &GovernanceConfig{
		Enabled:          true,
		ManagerURL:       "http://governance-manager:8080",
		ServiceName:      "diam-gw",
		PodIP:            "127.0.0.1",
		NotificationPort: 9002,
		Timeout:          -1 * time.Second,
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("Validate should fail when timeout is negative")
	}
}
