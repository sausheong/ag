package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sausheong/ag/config"
	harnesstool "github.com/sausheong/harness/tool"
)

// ConfigManageTool is the agent's instrument for reading and writing ag.yaml.
type ConfigManageTool struct {
	ConfigPath string
}

func (t *ConfigManageTool) Name() string { return "config_manage" }

func (t *ConfigManageTool) Description() string {
	return "Read or update ag.yaml configuration. Actions: get (read current config), " +
		"update_tool (add/replace a tool spec), update_skill (add/replace a skill entry), " +
		"set (update a top-level field like model)."
}

func (t *ConfigManageTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["get", "update_tool", "update_skill", "set"],
				"description": "Operation to perform."
			},
			"name": {"type": "string", "description": "Tool or skill name (for update_tool, update_skill)."},
			"description": {"type": "string", "description": "Human-readable description (for update_tool, update_skill)."},
			"parameters": {"type": "object", "description": "JSON Schema for the tool parameters (for update_tool)."},
			"positional": {
				"type": "array",
				"items": {"type": "string"},
				"description": "Parameter names that are positional args (not --flags), in order (for update_tool). IMPORTANT: always include this when the CLI takes positional arguments."
			},
			"examples": {
				"type": "array",
				"items": {"type": "string"},
				"description": "Example invocations (for update_tool)."
			},
			"body": {"type": "string", "description": "Skill body content (for update_skill)."},
			"type": {"type": "string", "enum": ["macro", "context"], "description": "Skill type (for update_skill)."},
			"field": {"type": "string", "enum": ["model", "auth_token", "base_url"], "description": "Top-level field name to update (for set)."},
			"value": {"type": "string", "description": "New value for the field (for set)."}
		},
		"required": ["action"]
	}`)
}

func (t *ConfigManageTool) IsConcurrencySafe(_ json.RawMessage) bool { return false }

type configInput struct {
	Action      string          `json:"action"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	Positional  []string        `json:"positional"`
	Examples    []string        `json:"examples"`
	Body        string          `json:"body"`
	Type        string          `json:"type"`
	Field       string          `json:"field"`
	Value       string          `json:"value"`
}

type configResult struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (t *ConfigManageTool) Execute(_ context.Context, raw json.RawMessage) (harnesstool.ToolResult, error) {
	var in configInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return errResult(fmt.Sprintf("invalid input: %v", err)), nil
	}
	switch in.Action {
	case "get":
		return t.execGet()
	case "update_tool":
		return t.execUpdateTool(in)
	case "update_skill":
		return t.execUpdateSkill(in)
	case "set":
		return t.execSet(in)
	default:
		return errResult(fmt.Sprintf("unknown action %q", in.Action)), nil
	}
}

func (t *ConfigManageTool) execGet() (harnesstool.ToolResult, error) {
	cfg, err := config.Load(t.ConfigPath)
	if err != nil {
		return errResult(err.Error()), nil
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return errResult(err.Error()), nil
	}
	return harnesstool.ToolResult{Output: string(data)}, nil
}

func (t *ConfigManageTool) execUpdateTool(in configInput) (harnesstool.ToolResult, error) {
	if in.Name == "" {
		return errResult("name is required for update_tool"), nil
	}
	cfg, err := config.Load(t.ConfigPath)
	if err != nil {
		return errResult(err.Error()), nil
	}
	// Preserve existing bin, hint, and positional if the update doesn't supply them.
	var existing config.ToolSpec
	for _, ts := range cfg.Tools {
		if ts.Name == in.Name {
			existing = ts
			break
		}
	}
	positional := in.Positional
	if len(positional) == 0 {
		positional = existing.Positional
	}

	spec := config.ToolSpec{
		Name:        in.Name,
		Bin:         existing.Bin,        // always preserved — agent must not change the binary path
		Hint:        existing.Hint,
		PrefixArgs:  existing.PrefixArgs, // preserved — agent must not change the prefix args
		Description: in.Description,
		Parameters:  in.Parameters,
		Positional:  positional,
		Examples:    in.Examples,
	}
	spec.URL      = existing.URL
	spec.Headless = existing.Headless
	spec.Profile  = existing.Profile
	updated := false
	for i, ts := range cfg.Tools {
		if ts.Name == in.Name {
			cfg.Tools[i] = spec
			updated = true
			break
		}
	}
	if !updated {
		cfg.Tools = append(cfg.Tools, spec)
	}
	if err := config.Save(t.ConfigPath, cfg); err != nil {
		return errResult(err.Error()), nil
	}
	action := "added"
	if updated {
		action = "updated"
	}
	return okResult(fmt.Sprintf("tool %q %s", in.Name, action)), nil
}

func (t *ConfigManageTool) execUpdateSkill(in configInput) (harnesstool.ToolResult, error) {
	if in.Name == "" {
		return errResult("name is required for update_skill"), nil
	}
	cfg, err := config.Load(t.ConfigPath)
	if err != nil {
		return errResult(err.Error()), nil
	}
	now := time.Now().UTC()
	entry := config.SkillEntry{
		Name:        in.Name,
		Description: in.Description,
		Body:        in.Body,
		Type:        in.Type,
		Origin:      "agent",
		UpdatedAt:   now,
	}
	updated := false
	for i, sk := range cfg.Skills {
		if sk.Name == in.Name {
			entry.CreatedAt = sk.CreatedAt
			cfg.Skills[i] = entry
			updated = true
			break
		}
	}
	if !updated {
		entry.CreatedAt = now
		cfg.Skills = append(cfg.Skills, entry)
	}
	if err := config.Save(t.ConfigPath, cfg); err != nil {
		return errResult(err.Error()), nil
	}
	action := "added"
	if updated {
		action = "updated"
	}
	return okResult(fmt.Sprintf("skill %q %s", in.Name, action)), nil
}

func (t *ConfigManageTool) execSet(in configInput) (harnesstool.ToolResult, error) {
	if in.Field == "" {
		return errResult("field is required for set"), nil
	}
	cfg, err := config.Load(t.ConfigPath)
	if err != nil {
		return errResult(err.Error()), nil
	}
	switch in.Field {
	case "model":
		cfg.Model = in.Value
	case "auth_token":
		cfg.AuthToken = in.Value
	case "base_url":
		cfg.BaseURL = in.Value
	default:
		return errResult(fmt.Sprintf("unsupported field %q", in.Field)), nil
	}
	if err := config.Save(t.ConfigPath, cfg); err != nil {
		return errResult(err.Error()), nil
	}
	return okResult(fmt.Sprintf("field %q set to %q", in.Field, in.Value)), nil
}

func okResult(msg string) harnesstool.ToolResult {
	b, _ := json.Marshal(configResult{Success: true, Message: msg})
	return harnesstool.ToolResult{Output: string(b)}
}

func errResult(msg string) harnesstool.ToolResult {
	b, _ := json.Marshal(configResult{Success: false, Error: msg})
	return harnesstool.ToolResult{Output: string(b)}
}

var _ harnesstool.Tool = (*ConfigManageTool)(nil)
