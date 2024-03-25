package db

import (
	"fmt"
	"testing"
)

const (
	notExistingFilePath = "not-existing-file-path"
)

func TestSSLNotEnabled(t *testing.T) {
	cfg := &Config{
		SSL: &SSLConfig{
			Enabled: false,
		},
	}

	if err := cfg.configureTLS(); err != nil {
		t.Fatalf("TLS configuration failed: %s", err)
	}

	if cfg.TLSConf != nil {
		t.Error("Expected TLSConf to be nil")
	}
}

func TestSSLEnabled(t *testing.T) {
	cfg := &Config{
		SSL: &SSLConfig{
			Enabled: true,
		},
	}

	if err := cfg.configureTLS(); err != nil {
		t.Fatalf("TLS configuration failed: %s", err)
	}

	if cfg.TLSConf == nil {
		t.Error("Expected TLSConf to not be nil")
	}
}

func TestPEMKeyFileDoesNotExists(t *testing.T) {
	cfg := &Config{
		SSL: &SSLConfig{
			Enabled:    true,
			PEMKeyFile: notExistingFilePath,
		},
	}

	err := cfg.configureTLS()
	if err == nil {
		t.Fatal("Expected TLS config to fail, but it returned no error")
	}

	expectedErrorMessage := fmt.Sprintf(
		"check if file with name %s exists: stat %s: no such file or directory",
		notExistingFilePath, notExistingFilePath,
	)
	if err.Error() != expectedErrorMessage {
		t.Errorf("error message '%s' does not match expected '%s'", err.Error(), expectedErrorMessage)
	}
}

func TestCAFileDoesNotExists(t *testing.T) {
	cfg := &Config{
		SSL: &SSLConfig{
			Enabled: true,
			CAFile:  notExistingFilePath,
		},
	}

	err := cfg.configureTLS()
	if err == nil {
		t.Fatal("Expected TLS config to fail, but it returned no error")
	}

	expectedErrorMessage := fmt.Sprintf(
		"check if file with name %s exists: stat %s: no such file or directory",
		notExistingFilePath, notExistingFilePath,
	)
	if err.Error() != expectedErrorMessage {
		t.Errorf("error message '%s' does not match expected '%s'", err.Error(), expectedErrorMessage)
	}
}
