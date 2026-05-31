package luckperms

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config holds the subset of LuckPerms config.yml relevant to a read-only,
// permission-resolving proxy node. Field names/tags mirror the LuckPerms config
// so an existing config.yml can be reused verbatim.
type Config struct {
	Server string `yaml:"server"`

	StorageMethod    string `yaml:"storage-method"`
	MessagingService string `yaml:"messaging-service"`

	Data struct {
		Address      string `yaml:"address"`
		Database     string `yaml:"database"`
		Username     string `yaml:"username"`
		Password     string `yaml:"password"`
		TablePrefix  string `yaml:"table-prefix"`
		PoolSettings struct {
			MaximumPoolSize int `yaml:"maximum-pool-size"`
		} `yaml:"pool-settings"`
	} `yaml:"data"`

	IncludeGlobal          bool `yaml:"include-global"`
	IncludeGlobalWorld     bool `yaml:"include-global-world"`
	ApplyGlobalGroups      bool `yaml:"apply-global-groups"`
	ApplyGlobalWorldGroups bool `yaml:"apply-global-world-groups"`

	ApplyWildcards       bool `yaml:"apply-wildcards"`
	ApplyWildcardsSponge bool `yaml:"apply-sponge-implicit-wildcards"`
	ApplyRegex           bool `yaml:"apply-regex"`

	// at-least-one-value-per-key (default) or all-values-per-key
	ContextSatisfyMode string `yaml:"context-satisfy-mode"`

	// OpPermission, if set, makes any player holding it an operator: permission
	// checks that would otherwise be undefined resolve to true. Explicit grants
	// and denials still take precedence. Set to "" to disable.
	OpPermission string `yaml:"op-permission"`
}

// defaultConfig returns a Config with the same defaults as LuckPerms.
func defaultConfig() *Config {
	c := &Config{
		Server:                 "global",
		StorageMethod:          "postgresql",
		MessagingService:       "auto",
		IncludeGlobal:          true,
		IncludeGlobalWorld:     true,
		ApplyGlobalGroups:      true,
		ApplyGlobalWorldGroups: true,
		ApplyWildcards:         true,
		ApplyWildcardsSponge:   false,
		ApplyRegex:             true,
		ContextSatisfyMode:     "at-least-one-value-per-key",
		OpPermission:           "gate.operator",
	}
	c.Data.Address = "localhost"
	c.Data.Database = "minecraft"
	c.Data.Username = "root"
	c.Data.Password = ""
	c.Data.TablePrefix = "luckperms_"
	c.Data.PoolSettings.MaximumPoolSize = 10
	return c
}

// LoadConfig reads config.yml at path, falling back to LuckPerms defaults for any
// missing keys. A missing file is an error (the operator must supply DB credentials).
func LoadConfig(path string) (*Config, error) {
	c := defaultConfig()

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	if err := yaml.Unmarshal(raw, c); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}

	// Validation of the actual storage/messaging backend happens against the
	// provider registry (see provider.go), so custom backends work too.
	if c.Data.TablePrefix == "" {
		c.Data.TablePrefix = "luckperms_"
	}
	return c, nil
}

// host and port parse the "address" field, which is "host" or "host:port".
func (c *Config) host() (host string, port int) {
	port = 5432
	addr := strings.TrimSpace(c.Data.Address)
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		host = addr[:i]
		if p, err := strconv.Atoi(addr[i+1:]); err == nil {
			port = p
		}
	} else {
		host = addr
	}
	if host == "" {
		host = "localhost"
	}
	return host, port
}

func (c *Config) requireAllContextValues() bool {
	return strings.EqualFold(strings.TrimSpace(c.ContextSatisfyMode), "all-values-per-key")
}
