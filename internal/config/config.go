package config

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Path     string
	Profiles map[string]Profile `toml:"profiles"`
}

type Profile struct {
	CPUHost       string   `toml:"cpu_host"`
	AuthHost      string   `toml:"auth_host"`
	User          string   `toml:"user"`
	AuthDomain    string   `toml:"auth_domain"`
	RemoteBase    string   `toml:"remote_base"`
	SecretEnv     string   `toml:"secret_env"`
	SecretCommand []string `toml:"secret_command"`
}

type Secret struct {
	Value  string
	Source string
}

func Load(path string) (*Config, error) {
	if path == "" {
		path = DefaultConfigPath()
	}
	cfg := &Config{}
	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, err
	}
	cfg.Path = path
	for name, profile := range cfg.Profiles {
		if profile.RemoteBase == "" && profile.User != "" {
			profile.RemoteBase = fmt.Sprintf("/usr/%s/agentic9/workspaces", profile.User)
		}
		cfg.Profiles[name] = profile
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) Validate() error {
	if len(c.Profiles) == 0 {
		return errors.New("config must define at least one profile")
	}
	for name, profile := range c.Profiles {
		if err := profile.Validate(); err != nil {
			return fmt.Errorf("profile %q: %w", name, err)
		}
	}
	return nil
}

func (c *Config) Profile(name string) (Profile, error) {
	profile, ok := c.Profiles[name]
	if !ok {
		return Profile{}, fmt.Errorf("profile %q not found", name)
	}
	return profile, nil
}

func (c *Config) LoadSecret(name string) (Secret, error) {
	profile, err := c.Profile(name)
	if err != nil {
		return Secret{}, err
	}
	switch {
	case profile.SecretEnv != "":
		value := os.Getenv(profile.SecretEnv)
		if value == "" {
			return Secret{}, fmt.Errorf(
				"secret env %q is empty for profile %q; export %s or update the profile to use secret_command",
				profile.SecretEnv,
				name,
				profile.SecretEnv,
			)
		}
		return Secret{Value: value, Source: "env:" + profile.SecretEnv}, nil
	case len(profile.SecretCommand) > 0:
		cmd := exec.Command(profile.SecretCommand[0], profile.SecretCommand[1:]...)
		out, err := cmd.Output()
		if err != nil {
			return Secret{}, fmt.Errorf(
				"secret command failed for profile %q (%s): %w",
				name,
				strings.Join(profile.SecretCommand, " "),
				err,
			)
		}
		return Secret{Value: strings.TrimSpace(string(out)), Source: "command"}, nil
	default:
		return Secret{}, errors.New("profile has no secret source")
	}
}

func (p Profile) Validate() error {
	missing := make([]string, 0, 4)
	if p.CPUHost == "" {
		missing = append(missing, "cpu_host")
	}
	if p.AuthHost == "" {
		missing = append(missing, "auth_host")
	}
	if p.User == "" {
		missing = append(missing, "user")
	}
	if p.AuthDomain == "" {
		missing = append(missing, "auth_domain")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required fields: %s", strings.Join(missing, ", "))
	}
	if (p.SecretEnv == "") == (len(p.SecretCommand) == 0) {
		return errors.New("exactly one of secret_env or secret_command must be set")
	}
	return nil
}

func DefaultConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return filepath.Join(".config", "agentic9", "config.toml")
	}
	return filepath.Join(dir, "agentic9", "config.toml")
}

func DefaultRuntimeRoot() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "agentic9")
	}
	return filepath.Join(os.TempDir(), "agentic9-runtime")
}

func (c *Config) StateRoot() string {
	dir, err := os.UserHomeDir()
	if err != nil {
		return ".agentic9-state"
	}
	return filepath.Join(dir, ".local", "state", "agentic9", "workspaces")
}

func (c *Config) RuntimeRoot() string {
	return DefaultRuntimeRoot()
}
