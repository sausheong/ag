package tool_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sausheong/ag/config"
	agtool "github.com/sausheong/ag/tool"
	"github.com/sausheong/harness/tool/skills"
)

func newSkillStore(t *testing.T) (*agtool.JSONSkillStore, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ag.yaml")
	cfg := &config.Config{Model: "m", Skills: []config.SkillEntry{}}
	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return &agtool.JSONSkillStore{ConfigPath: path}, path
}

func TestJSONSkillStoreCreateAndGet(t *testing.T) {
	store, _ := newSkillStore(t)
	ctx := context.Background()

	created, err := store.Create(ctx, skills.Skill{Name: "my-skill", Body: "do the thing"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Name != "my-skill" {
		t.Errorf("Name: %q", created.Name)
	}

	got, ok, err := store.Get(ctx, "my-skill")
	if err != nil || !ok {
		t.Fatalf("Get: err=%v ok=%v", err, ok)
	}
	if got.Body != "do the thing" {
		t.Errorf("Body: %q", got.Body)
	}
}

func TestJSONSkillStoreAlreadyExists(t *testing.T) {
	store, _ := newSkillStore(t)
	ctx := context.Background()
	_, _ = store.Create(ctx, skills.Skill{Name: "dupe", Body: "first"})
	_, err := store.Create(ctx, skills.Skill{Name: "dupe", Body: "second"})
	if err == nil {
		t.Fatal("expected ErrAlreadyExists")
	}
}

func TestJSONSkillStoreList(t *testing.T) {
	store, _ := newSkillStore(t)
	ctx := context.Background()
	_, _ = store.Create(ctx, skills.Skill{Name: "b-skill", Body: "b"})
	_, _ = store.Create(ctx, skills.Skill{Name: "a-skill", Body: "a"})
	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(list))
	}
	if list[0].Name != "a-skill" {
		t.Errorf("expected sorted: got %q first", list[0].Name)
	}
}

func TestJSONSkillStoreRemove(t *testing.T) {
	store, _ := newSkillStore(t)
	ctx := context.Background()
	_, _ = store.Create(ctx, skills.Skill{Name: "to-remove", Body: "x"})
	if err := store.Remove(ctx, "to-remove"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	_, ok, _ := store.Get(ctx, "to-remove")
	if ok {
		t.Error("skill still present after remove")
	}
}

func TestJSONSkillStoreFormatIndex(t *testing.T) {
	store, _ := newSkillStore(t)
	ctx := context.Background()
	_, _ = store.Create(ctx, skills.Skill{Name: "my-skill", Body: "---\ndescription: does stuff\n---\nbody"})
	idx := store.FormatIndex()
	if idx == "" {
		t.Error("FormatIndex returned empty string with skills present")
	}
}

func TestJSONSkillStoreInvalidName(t *testing.T) {
	store, _ := newSkillStore(t)
	ctx := context.Background()
	_, err := store.Create(ctx, skills.Skill{Name: "INVALID NAME", Body: "body"})
	if err == nil {
		t.Fatal("expected ErrInvalidName")
	}
}

func TestJSONSkillStorePatch(t *testing.T) {
	store, _ := newSkillStore(t)
	ctx := context.Background()
	_, err := store.Create(ctx, skills.Skill{Name: "patch-me", Body: "hello world"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated, err := store.Patch(ctx, "patch-me", "hello", "goodbye")
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if updated.Body != "goodbye world" {
		t.Errorf("Patch body: %q", updated.Body)
	}
}

func TestJSONSkillStorePatchNotFound(t *testing.T) {
	store, _ := newSkillStore(t)
	ctx := context.Background()
	_, err := store.Patch(ctx, "nonexistent", "old", "new")
	if err == nil {
		t.Fatal("expected ErrNotFound")
	}
}

func TestJSONSkillStorePatchNoMatch(t *testing.T) {
	store, _ := newSkillStore(t)
	ctx := context.Background()
	_, _ = store.Create(ctx, skills.Skill{Name: "patch-me", Body: "hello world"})
	_, err := store.Patch(ctx, "patch-me", "xyz", "abc")
	if err == nil {
		t.Fatal("expected ErrPatchNoMatch")
	}
}

func TestJSONSkillStorePatchIdentical(t *testing.T) {
	store, _ := newSkillStore(t)
	ctx := context.Background()
	_, _ = store.Create(ctx, skills.Skill{Name: "patch-me", Body: "hello world"})
	_, err := store.Patch(ctx, "patch-me", "hello", "hello")
	if err == nil {
		t.Fatal("expected ErrPatchIdentical")
	}
}

func TestJSONSkillStoreReplace(t *testing.T) {
	store, _ := newSkillStore(t)
	ctx := context.Background()
	_, _ = store.Create(ctx, skills.Skill{Name: "replace-me", Body: "old body"})
	updated, err := store.Replace(ctx, "replace-me", "new body")
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if updated.Body != "new body" {
		t.Errorf("Replace body: %q", updated.Body)
	}
}

func TestJSONSkillStoreReplaceNotFound(t *testing.T) {
	store, _ := newSkillStore(t)
	ctx := context.Background()
	_, err := store.Replace(ctx, "nonexistent", "body")
	if err == nil {
		t.Fatal("expected ErrNotFound")
	}
}

func TestSkillProviderAdapter(t *testing.T) {
	store, _ := newSkillStore(t)
	ctx := context.Background()
	_, _ = store.Create(ctx, skills.Skill{Name: "adapter-skill", Body: "adapter body"})

	adapter := &agtool.SkillProviderAdapter{Store: store}

	body, ok := adapter.Get("adapter-skill")
	if !ok {
		t.Fatal("expected adapter.Get to find skill")
	}
	if body != "adapter body" {
		t.Errorf("adapter body: %q", body)
	}

	idx := adapter.FormatIndex()
	if idx == "" {
		t.Error("FormatIndex returned empty string with skills present")
	}

	_, ok = adapter.Get("nonexistent")
	if ok {
		t.Error("expected adapter.Get to return false for nonexistent skill")
	}
}
