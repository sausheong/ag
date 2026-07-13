package tool

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sausheong/ag/config"
	"github.com/sausheong/harness/runtime"
	"github.com/sausheong/harness/tool/skills"
)

// JSONSkillStore implements skills.SkillStore backed by ag.yaml's skills array.
// It is safe for concurrent use within a single process. Cross-process safety
// is not guaranteed — callers should not assume file-lock semantics.
//
// To satisfy runtime.SkillProvider (which has Get(name string) (string, bool)
// — a different signature from SkillStore.Get), use SkillProviderAdapter.
type JSONSkillStore struct {
	ConfigPath string
	mu         sync.Mutex
}

func (s *JSONSkillStore) load() (*config.Config, error) {
	return config.Load(s.ConfigPath)
}

func (s *JSONSkillStore) save(cfg *config.Config) error {
	return config.Save(s.ConfigPath, cfg)
}

func entryToSkill(e config.SkillEntry) skills.Skill {
	return skills.Skill{
		Name:        e.Name,
		Description: e.Description,
		Body:        e.Body,
		Tags:        e.Tags,
		CreatedAt:   e.CreatedAt,
		UpdatedAt:   e.UpdatedAt,
		Origin:      e.Origin,
	}
}

func skillToEntry(sk skills.Skill) config.SkillEntry {
	return config.SkillEntry{
		Name:        sk.Name,
		Description: sk.Description,
		Body:        sk.Body,
		Tags:        sk.Tags,
		CreatedAt:   sk.CreatedAt,
		UpdatedAt:   sk.UpdatedAt,
		Origin:      sk.Origin,
	}
}

// Create writes a new skill. Rejects ErrAlreadyExists if name already exists;
// ErrInvalidName if name fails ValidName. Sets CreatedAt and UpdatedAt; sets
// Origin to "agent" if empty.
func (s *JSONSkillStore) Create(_ context.Context, sk skills.Skill) (skills.Skill, error) {
	if !skills.ValidName(sk.Name) {
		return skills.Skill{}, skills.ErrInvalidName
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, err := s.load()
	if err != nil {
		return skills.Skill{}, err
	}
	for _, e := range cfg.Skills {
		if e.Name == sk.Name {
			return skills.Skill{}, skills.ErrAlreadyExists
		}
	}
	now := time.Now().UTC()
	sk.CreatedAt = now
	sk.UpdatedAt = now
	if sk.Origin == "" {
		sk.Origin = "agent"
	}
	cfg.Skills = append(cfg.Skills, skillToEntry(sk))
	if err := s.save(cfg); err != nil {
		return skills.Skill{}, err
	}
	return sk, nil
}

// Patch replaces the first occurrence of oldContent with newContent inside the
// existing skill's Body. Returns ErrPatchIdentical when old == new,
// ErrNotFound when name is unknown, ErrPatchNoMatch when oldContent is absent,
// and ErrPatchAmbiguous when oldContent matches more than once.
func (s *JSONSkillStore) Patch(_ context.Context, name, oldContent, newContent string) (skills.Skill, error) {
	if oldContent == newContent {
		return skills.Skill{}, skills.ErrPatchIdentical
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, err := s.load()
	if err != nil {
		return skills.Skill{}, err
	}
	for i, e := range cfg.Skills {
		if e.Name != name {
			continue
		}
		count := strings.Count(e.Body, oldContent)
		if count == 0 {
			return skills.Skill{}, skills.ErrPatchNoMatch
		}
		if count > 1 {
			return skills.Skill{}, fmt.Errorf("%w: matched %d times", skills.ErrPatchAmbiguous, count)
		}
		cfg.Skills[i].Body = strings.Replace(e.Body, oldContent, newContent, 1)
		cfg.Skills[i].UpdatedAt = time.Now().UTC()
		if err := s.save(cfg); err != nil {
			return skills.Skill{}, err
		}
		return entryToSkill(cfg.Skills[i]), nil
	}
	return skills.Skill{}, skills.ErrNotFound
}

// Replace overwrites the entire body of an existing skill. Returns ErrNotFound
// when name is unknown. Preserves CreatedAt; refreshes UpdatedAt.
func (s *JSONSkillStore) Replace(_ context.Context, name, body string) (skills.Skill, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, err := s.load()
	if err != nil {
		return skills.Skill{}, err
	}
	for i, e := range cfg.Skills {
		if e.Name == name {
			cfg.Skills[i].Body = body
			cfg.Skills[i].UpdatedAt = time.Now().UTC()
			if err := s.save(cfg); err != nil {
				return skills.Skill{}, err
			}
			return entryToSkill(cfg.Skills[i]), nil
		}
	}
	return skills.Skill{}, skills.ErrNotFound
}

// Remove deletes a skill. Idempotent — removing an unknown name returns nil.
func (s *JSONSkillStore) Remove(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, err := s.load()
	if err != nil {
		return err
	}
	out := cfg.Skills[:0]
	for _, e := range cfg.Skills {
		if e.Name != name {
			out = append(out, e)
		}
	}
	cfg.Skills = out
	return s.save(cfg)
}

// List returns all skills sorted by Name.
func (s *JSONSkillStore) List(_ context.Context) ([]skills.Skill, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, err := s.load()
	if err != nil {
		return nil, err
	}
	out := make([]skills.Skill, len(cfg.Skills))
	for i, e := range cfg.Skills {
		out[i] = entryToSkill(e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Get returns one skill by name. The bool is false (no error) when the name is
// unknown.
func (s *JSONSkillStore) Get(_ context.Context, name string) (skills.Skill, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, err := s.load()
	if err != nil {
		return skills.Skill{}, false, err
	}
	for _, e := range cfg.Skills {
		if e.Name == name {
			return entryToSkill(e), true, nil
		}
	}
	return skills.Skill{}, false, nil
}

// FormatIndex returns a text index of all skills suitable for inclusion in the
// system prompt. Implements the FormatIndex method shared by runtime.SkillProvider.
func (s *JSONSkillStore) FormatIndex() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, err := s.load()
	if err != nil || len(cfg.Skills) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Available Skills\n\n")
	for _, e := range cfg.Skills {
		desc := e.Description
		if desc == "" {
			desc = "(no description)"
		}
		fmt.Fprintf(&b, "- **%s** (%s): %s\n", e.Name, e.Type, desc)
	}
	return b.String()
}

// getBody is a mutex-safe helper for looking up a skill body by name.
// Used by SkillProviderAdapter to avoid bypassing the store's mutex.
func (s *JSONSkillStore) getBody(name string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, err := s.load()
	if err != nil {
		return "", false
	}
	for _, e := range cfg.Skills {
		if e.Name == name {
			return e.Body, true
		}
	}
	return "", false
}

// Verify JSONSkillStore satisfies skills.SkillStore at compile time.
var _ skills.SkillStore = (*JSONSkillStore)(nil)

// Verify SkillProviderAdapter satisfies runtime.SkillProvider at compile time.
var _ runtime.SkillProvider = (*SkillProviderAdapter)(nil)

// SkillProviderAdapter wraps JSONSkillStore to satisfy runtime.SkillProvider.
// runtime.SkillProvider.Get has signature (name string) (string, bool), which
// conflicts with skills.SkillStore.Get's (context.Context, string) (Skill, bool, error).
// Go does not allow both on the same struct, so this adapter bridges the gap.
type SkillProviderAdapter struct {
	Store *JSONSkillStore
}

// FormatIndex delegates to the underlying store.
func (a *SkillProviderAdapter) FormatIndex() string {
	return a.Store.FormatIndex()
}

// Get implements runtime.SkillProvider: looks up a skill by name and returns
// its body. Returns ("", false) on any error or when the name is unknown.
func (a *SkillProviderAdapter) Get(name string) (string, bool) {
	return a.Store.getBody(name)
}
