package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// ErrNotFound is returned by Load when ag.yaml does not exist.
var ErrNotFound = errors.New("ag.yaml not found — create a skeleton:\nmodel: anthropic/claude-sonnet-4-5\ntools: []\nskills: []")

// ToolSpec is the single definition of a CLI tool. You write name+bin+hint;
// bootstrap enriches it with description, parameters, positional, and examples.
type ToolSpec struct {
	Name        string `yaml:"name"             json:"name"`
	Bin         string `yaml:"bin"              json:"bin"`
	Hint        string `yaml:"hint,omitempty"   json:"hint,omitempty"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`

	// Parameters is JSON Schema as raw JSON internally; native YAML mapping on disk.
	Parameters json.RawMessage `yaml:"-" json:"parameters,omitempty"`
	// Positional lists parameter names passed as bare positional args, in order.
	Positional []string `yaml:"positional,omitempty"    json:"positional,omitempty"`
	// PrefixArgs are fixed args prepended before any user-supplied args.
	// Useful for tools that require a subcommand (e.g. bash -c, python3 -c).
	PrefixArgs []string `yaml:"prefix_args,omitempty"   json:"prefix_args,omitempty"`
	Examples   []string `yaml:"examples,omitempty"       json:"examples,omitempty"`
	URL        string   `yaml:"url,omitempty"            json:"url,omitempty"`
	Headless   bool     `yaml:"headless,omitempty"       json:"headless,omitempty"`
	Profile    string   `yaml:"profile,omitempty"        json:"profile,omitempty"`
}

// Bootstrapped reports whether this tool has been enriched by bootstrap.
func (ts ToolSpec) Bootstrapped() bool {
	return ts.Description != "" || ts.Parameters != nil
}

// IsWeb reports whether this tool is a web-based tool (has a URL).
func (ts ToolSpec) IsWeb() bool {
	return ts.URL != ""
}

// yamlToolSpec is the YAML-serialisable form of ToolSpec.
type yamlToolSpec struct {
	Name        string   `yaml:"name"`
	Bin         string   `yaml:"bin"`
	Hint        string   `yaml:"hint,omitempty"`
	Description string   `yaml:"description,omitempty"`
	Parameters  any      `yaml:"parameters,omitempty"`
	Positional  []string `yaml:"positional,omitempty"`
	PrefixArgs  []string `yaml:"prefix_args,omitempty"`
	Examples    []string `yaml:"examples,omitempty"`
	URL         string   `yaml:"url,omitempty"`
	Headless    bool     `yaml:"headless,omitempty"`
	Profile     string   `yaml:"profile,omitempty"`
}

func (ts ToolSpec) toYAML() yamlToolSpec {
	var params any
	if len(ts.Parameters) > 0 {
		_ = json.Unmarshal(ts.Parameters, &params)
	}
	return yamlToolSpec{
		Name:        ts.Name,
		Bin:         ts.Bin,
		Hint:        ts.Hint,
		Description: ts.Description,
		Parameters:  params,
		Positional:  ts.Positional,
		PrefixArgs:  ts.PrefixArgs,
		Examples:    ts.Examples,
		URL:         ts.URL,
		Headless:    ts.Headless,
		Profile:     ts.Profile,
	}
}

func toolSpecFromYAML(y yamlToolSpec) (ToolSpec, error) {
	ts := ToolSpec{
		Name:        y.Name,
		Bin:         y.Bin,
		Hint:        y.Hint,
		Description: y.Description,
		Positional:  y.Positional,
		PrefixArgs:  y.PrefixArgs,
		Examples:    y.Examples,
		URL:         y.URL,
		Headless:    y.Headless,
		Profile:     y.Profile,
	}
	if y.Parameters != nil {
		b, err := json.Marshal(y.Parameters)
		if err != nil {
			return ts, fmt.Errorf("converting parameters to JSON: %w", err)
		}
		ts.Parameters = json.RawMessage(b)
	}
	if ts.Bin != "" && ts.URL != "" {
		return ts, fmt.Errorf("tool %q: cannot set both 'bin' and 'url' — use one or the other", ts.Name)
	}
	return ts, nil
}

// MCPServerConfig describes one MCP server to connect to.
// Exactly one transport must be set: Command (stdio) or URL (HTTP).
type MCPServerConfig struct {
	Name    string            `yaml:"name"              json:"name"`
	Command string            `yaml:"command,omitempty" json:"command,omitempty"`
	Args    []string          `yaml:"args,omitempty"    json:"args,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"     json:"env,omitempty"`
	URL     string            `yaml:"url,omitempty"     json:"url,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
}

type SkillEntry struct {
	Name        string    `yaml:"name"                  json:"name"`
	Description string    `yaml:"description,omitempty" json:"description,omitempty"`
	Body        string    `yaml:"body,omitempty"        json:"body,omitempty"`
	Type        string    `yaml:"type,omitempty"        json:"type,omitempty"`
	Tags        []string  `yaml:"tags,omitempty"        json:"tags,omitempty"`
	CreatedAt   time.Time `yaml:"created_at"            json:"created_at"`
	UpdatedAt   time.Time `yaml:"updated_at"            json:"updated_at"`
	Origin      string    `yaml:"origin,omitempty"      json:"origin,omitempty"`
}

// Config is the in-memory representation of ag.yaml.
type Config struct {
	Model      string            `yaml:"model"                 json:"model"`
	AuthToken  string            `yaml:"auth_token,omitempty"  json:"auth_token,omitempty"`
	BaseURL    string            `yaml:"base_url,omitempty"    json:"base_url,omitempty"`
	Tools      []ToolSpec        `yaml:"tools"                 json:"tools"`
	MCPServers []MCPServerConfig `yaml:"mcp_servers,omitempty" json:"mcp_servers,omitempty"`
	Skills     []SkillEntry      `yaml:"skills"                json:"skills"`
}

// yamlConfig mirrors Config but uses yamlToolSpec for Tools.
type yamlConfig struct {
	Model      string            `yaml:"model"`
	AuthToken  string            `yaml:"auth_token,omitempty"`
	BaseURL    string            `yaml:"base_url,omitempty"`
	Tools      []yamlToolSpec    `yaml:"tools"`
	MCPServers []MCPServerConfig `yaml:"mcp_servers,omitempty"`
	Skills     []SkillEntry      `yaml:"skills"`
}

// Load reads ag.yaml at path. Returns ErrNotFound if the file is absent.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reading ag.yaml: %w", err)
	}
	var yc yamlConfig
	if err := yaml.Unmarshal(data, &yc); err != nil {
		return nil, fmt.Errorf("parsing ag.yaml: %w", err)
	}
	cfg := &Config{
		Model:      yc.Model,
		AuthToken:  yc.AuthToken,
		BaseURL:    yc.BaseURL,
		MCPServers: yc.MCPServers,
		Skills:     yc.Skills,
	}
	for _, yt := range yc.Tools {
		ts, err := toolSpecFromYAML(yt)
		if err != nil {
			return nil, err
		}
		cfg.Tools = append(cfg.Tools, ts)
	}
	if cfg.Tools == nil {
		cfg.Tools = []ToolSpec{}
	}
	if cfg.Skills == nil {
		cfg.Skills = []SkillEntry{}
	}
	return cfg, nil
}

// Save atomically writes cfg to path as YAML (temp file + rename).
func Save(path string, cfg *Config) error {
	yc := yamlConfig{
		Model:      cfg.Model,
		AuthToken:  cfg.AuthToken,
		BaseURL:    cfg.BaseURL,
		MCPServers: cfg.MCPServers,
		Skills:     cfg.Skills,
	}
	for _, ts := range cfg.Tools {
		yc.Tools = append(yc.Tools, ts.toYAML())
	}
	data, err := yaml.Marshal(yc)
	if err != nil {
		return fmt.Errorf("marshalling config: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("renaming temp file: %w", err)
	}
	return nil
}
