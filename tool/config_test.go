package tool_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sausheong/ag/config"
	agtool "github.com/sausheong/ag/tool"
)

func writeCfg(t *testing.T, dir string, cfg *config.Config) string {
	t.Helper()
	path := filepath.Join(dir, "ag.yaml")
	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("writeCfg: %v", err)
	}
	return path
}

func TestConfigManageGet(t *testing.T) {
	dir := t.TempDir()
	path := writeCfg(t, dir, &config.Config{
		Model: "anthropic/claude-sonnet-4-5",
		Tools: []config.ToolSpec{{Name: "git", Bin: "git"}},
	})

	tool := &agtool.ConfigManageTool{ConfigPath: path}
	input, _ := json.Marshal(map[string]string{"action": "get"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result.Output), &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if out["model"] != "anthropic/claude-sonnet-4-5" {
		t.Errorf("model: got %v", out["model"])
	}
}

func TestConfigManageUpdateTool(t *testing.T) {
	dir := t.TempDir()
	path := writeCfg(t, dir, &config.Config{Model: "m", Tools: []config.ToolSpec{}})

	tool := &agtool.ConfigManageTool{ConfigPath: path}
	input, _ := json.Marshal(map[string]any{
		"action":      "update_tool",
		"name":        "git",
		"description": "Git version control",
		"parameters":  json.RawMessage(`{"type":"object","properties":{"args":{"type":"string"}}}`),
	})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result.Output), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out["success"] != true {
		t.Errorf("expected success, got %v", out)
	}
	// Verify persisted
	cfg, _ := config.Load(path)
	if len(cfg.Tools) != 1 || cfg.Tools[0].Name != "git" {
		t.Errorf("tool not persisted: %+v", cfg.Tools)
	}
}

func TestConfigManageUpdateSkill(t *testing.T) {
	dir := t.TempDir()
	path := writeCfg(t, dir, &config.Config{Model: "m", Skills: []config.SkillEntry{}})

	tool := &agtool.ConfigManageTool{ConfigPath: path}
	input, _ := json.Marshal(map[string]any{
		"action":      "update_skill",
		"name":        "deploy-staging",
		"description": "Deploy to staging",
		"body":        "run build then rsync",
		"type":        "macro",
	})
	_, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	cfg, _ := config.Load(path)
	if len(cfg.Skills) != 1 || cfg.Skills[0].Name != "deploy-staging" {
		t.Errorf("skill not persisted: %+v", cfg.Skills)
	}
	if cfg.Skills[0].CreatedAt.IsZero() {
		t.Error("CreatedAt should be set on new skill entry")
	}
	if cfg.Skills[0].UpdatedAt.IsZero() {
		t.Error("UpdatedAt should be set on new skill entry")
	}
}

func TestConfigManageSet(t *testing.T) {
	dir := t.TempDir()
	path := writeCfg(t, dir, &config.Config{Model: "old-model"})

	tool := &agtool.ConfigManageTool{ConfigPath: path}
	input, _ := json.Marshal(map[string]any{
		"action": "set",
		"field":  "model",
		"value":  "anthropic/claude-opus-4-8",
	})
	_, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	cfg, _ := config.Load(path)
	if cfg.Model != "anthropic/claude-opus-4-8" {
		t.Errorf("model not updated: %q", cfg.Model)
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
