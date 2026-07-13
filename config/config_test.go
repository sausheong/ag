package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sausheong/ag/config"
	"gopkg.in/yaml.v3"
)

func TestLoadSaveRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ag.yaml")

	original := &config.Config{
		Model: "anthropic/claude-sonnet-4-5",
		Tools: []config.ToolSpec{
			{Name: "git", Bin: "git", Hint: "focus on branch ops"},
		},
		Skills: []config.SkillEntry{},
	}

	if err := config.Save(path, original); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.Model != original.Model {
		t.Errorf("Model: got %q want %q", loaded.Model, original.Model)
	}
	if len(loaded.Tools) != 1 || loaded.Tools[0].Name != "git" {
		t.Errorf("Tools mismatch: %+v", loaded.Tools)
	}
	if loaded.Tools[0].Bin != "git" {
		t.Errorf("Bin not preserved: %q", loaded.Tools[0].Bin)
	}
}

func TestLoadNotFound(t *testing.T) {
	_, err := config.Load("/nonexistent/ag.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestSaveAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ag.yaml")

	cfg := &config.Config{Model: "test", Tools: nil, Skills: nil}
	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var check config.Config
	if err := yaml.Unmarshal(data, &check); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if check.Model != "test" {
		t.Errorf("Model: got %q want %q", check.Model, "test")
	}
}

func TestSkillEntryRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ag.yaml")

	cfg := &config.Config{
		Model: "anthropic/claude-sonnet-4-5",
		Skills: []config.SkillEntry{
			{
				Name:        "deploy-staging",
				Description: "Deploy to staging",
				Body:        "run build then rsync",
				Type:        "macro",
				CreatedAt:   time.Now().UTC().Truncate(time.Second),
			},
		},
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Skills) != 1 || loaded.Skills[0].Name != "deploy-staging" {
		t.Errorf("Skills mismatch: %+v", loaded.Skills)
	}
}

func TestToolSpecWebRoundtrip(t *testing.T) {
	cfg := &config.Config{
		Model: "test/model",
		Tools: []config.ToolSpec{
			{Name: "gmail", URL: "https://mail.google.com", Headless: false, Profile: "work"},
		},
		Skills: []config.SkillEntry{},
	}
	tmp := t.TempDir() + "/ag.yaml"
	if err := config.Save(tmp, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	tool := got.Tools[0]
	if tool.URL != "https://mail.google.com" {
		t.Errorf("URL: got %q want %q", tool.URL, "https://mail.google.com")
	}
	if tool.Headless != false {
		t.Error("Headless should be false")
	}
	if tool.Profile != "work" {
		t.Errorf("Profile: got %q want %q", tool.Profile, "work")
	}
	if !tool.IsWeb() {
		t.Error("IsWeb() should return true")
	}
}
