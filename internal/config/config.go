// Package config parses RIFT agent configuration from NDK notifications.
// NDK delivers config as multiple notifications:
//   - ".rift" with JSON data for scalar fields (admin-state, system-id, level)
//   - ".rift.interface{.name==\"ethernet-1/X\"}" for each interface (empty data)
package config

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hyposcaler/srl-rift/internal/encoding"
)

// Config holds the RIFT agent configuration.
type Config struct {
	AdminState string // "enable" or "disable"
	SystemID   encoding.SystemIDType
	Level      encoding.LevelType
	Interfaces map[string]struct{} // set of SRL interface names
}

// riftJSON matches the JSON structure delivered by NDK at path ".rift".
// Field names use YANG naming (hyphens). system-id is delivered as a string.
type riftJSON struct {
	AdminState *string `json:"admin-state"`
	SystemID   *string `json:"system-id"`
	Level      *int8   `json:"level"`
}

// ParseRiftData parses the JSON data from the ".rift" notification.
func ParseRiftData(data string) (*Config, error) {
	var raw riftJSON
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	cfg := &Config{
		AdminState: "enable",
		Interfaces: make(map[string]struct{}),
	}

	if raw.AdminState != nil {
		cfg.AdminState = *raw.AdminState
	}
	if raw.SystemID != nil {
		var id uint64
		if _, err := fmt.Sscanf(*raw.SystemID, "%d", &id); err != nil {
			return nil, fmt.Errorf("parse system-id %q: %w", *raw.SystemID, err)
		}
		cfg.SystemID = encoding.SystemIDType(id)
	}
	if raw.Level != nil {
		cfg.Level = encoding.LevelType(*raw.Level)
	}

	return cfg, nil
}

// ExtractInterfaceName extracts the interface name from a js_path_with_keys
// like ".rift.interface{.name==\"ethernet-1/1\"}".
func ExtractInterfaceName(pathWithKeys string) (string, bool) {
	const prefix = `.rift.interface{.name=="`
	const suffix = `"}`
	if !strings.HasPrefix(pathWithKeys, prefix) || !strings.HasSuffix(pathWithKeys, suffix) {
		return "", false
	}
	name := pathWithKeys[len(prefix) : len(pathWithKeys)-len(suffix)]
	return name, true
}

// Valid returns true if the config has the minimum required fields.
func (c *Config) Valid() bool {
	return c.SystemID != 0 && len(c.Interfaces) > 0
}

// HasInterface returns true if the given SRL interface name is configured.
func (c *Config) HasInterface(name string) bool {
	_, ok := c.Interfaces[name]
	return ok
}
