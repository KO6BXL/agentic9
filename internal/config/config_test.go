package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateSecretSources(t *testing.T) {
	cfg := &Config{Profiles: map[string]Profile{
		"default": {
			CPUHost:    "cpu",
			AuthHost:   "auth",
			User:       "glenda",
			AuthDomain: "example.net",
			SecretEnv:  "A",
		},
	}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	cfg.Profiles["default"] = Profile{
		CPUHost:       "cpu",
		AuthHost:      "auth",
		User:          "glenda",
		AuthDomain:    "example.net",
		SecretEnv:     "A",
		SecretCommand: []string{"printenv", "A"},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestLoadSecretFromEnv(t *testing.T) {
	t.Setenv("AGENTIC9_SECRET_TEST", "s3cret")
	cfg := &Config{Profiles: map[string]Profile{
		"default": {
			CPUHost:    "cpu",
			AuthHost:   "auth",
			User:       "glenda",
			AuthDomain: "example.net",
			SecretEnv:  "AGENTIC9_SECRET_TEST",
		},
	}}
	secret, err := cfg.LoadSecret("default")
	if err != nil {
		t.Fatalf("LoadSecret: %v", err)
	}
	if secret.Value != "s3cret" {
		t.Fatalf("unexpected secret: %q", secret.Value)
	}
}

func TestLoadSecretFromMissingEnvIncludesRecoveryHint(t *testing.T) {
	cfg := &Config{Profiles: map[string]Profile{
		"default": {
			CPUHost:    "cpu",
			AuthHost:   "auth",
			User:       "glenda",
			AuthDomain: "example.net",
			SecretEnv:  "AGENTIC9_SECRET_TEST",
		},
	}}
	_, err := cfg.LoadSecret("default")
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, `secret env "AGENTIC9_SECRET_TEST" is empty for profile "default"`) {
		t.Fatalf("unexpected error: %q", msg)
	}
	if !strings.Contains(msg, "export AGENTIC9_SECRET_TEST") {
		t.Fatalf("missing export hint: %q", msg)
	}
	if !strings.Contains(msg, "secret_command") {
		t.Fatalf("missing secret_command hint: %q", msg)
	}
}

func TestLoadConfigFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(`
[profiles.default]
cpu_host = "cpu"
auth_host = "auth"
user = "glenda"
auth_domain = "example.net"
secret_env = "AGENTIC9_SECRET_TEST"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	profile, err := cfg.Profile("default")
	if err != nil {
		t.Fatalf("Profile: %v", err)
	}
	if profile.RemoteBase != "/usr/glenda/agentic9/workspaces" {
		t.Fatalf("unexpected remote base: %q", profile.RemoteBase)
	}
}
