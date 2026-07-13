package tool_test

import (
	"os"
	"strings"
	"testing"

	"github.com/sausheong/ag/tool"
)

func TestProfileDir(t *testing.T) {
	dir, err := tool.ProfileDir("testprofile-abc123")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(dir, "/.ag/profiles/testprofile-abc123") {
		t.Errorf("unexpected path: %q", dir)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}
	os.RemoveAll(dir)
}

func TestProfileDirDefault(t *testing.T) {
	dir, err := tool.ProfileDir("")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(dir, "/.ag/profiles/default") {
		t.Errorf("unexpected path: %q", dir)
	}
	os.RemoveAll(dir)
}
