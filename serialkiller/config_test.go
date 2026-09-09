package serialkiller

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Port of org.nibblesec.tools.ConfigurationTest.

// testCreateNull: new Configuration(null) -> IllegalStateException.
func TestConfiguration_CreateNull(t *testing.T) {
	_, err := NewConfiguration("")
	if err == nil {
		t.Fatal("expected error for empty/null path, got nil")
	}
	var ise *IllegalStateError
	if !errors.As(err, &ise) {
		t.Fatalf("expected *IllegalStateError, got %T: %v", err, err)
	}
}

// testCreateNonExistant: new Configuration("/i/am/pretty-sure/this-file/does-not-exist")
// -> IllegalStateException.
func TestConfiguration_CreateNonExistant(t *testing.T) {
	_, err := NewConfiguration("/i/am/pretty-sure/this-file/does-not-exist")
	if err == nil {
		t.Fatal("expected error for non-existent file, got nil")
	}
	var ise *IllegalStateError
	if !errors.As(err, &ise) {
		t.Fatalf("expected *IllegalStateError, got %T: %v", err, err)
	}
}

// testCreateNonConfig: empty temp file -> IllegalStateException.
func TestConfiguration_CreateNonConfig(t *testing.T) {
	tmp, err := os.CreateTemp("", "serialkiller-*.tmp")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmp.Name())
	tmp.Close()

	_, err = NewConfiguration(tmp.Name())
	if err == nil {
		t.Fatal("expected error for empty (non-config) file, got nil")
	}
	var ise *IllegalStateError
	if !errors.As(err, &ise) {
		t.Fatalf("expected *IllegalStateError, got %T: %v", err, err)
	}
}

// testCreateBadPattern: broken-pattern.conf -> IllegalStateException.
func TestConfiguration_CreateBadPattern(t *testing.T) {
	_, err := NewConfiguration("../testdata/broken-pattern.conf")
	if err == nil {
		t.Fatal("expected error for broken pattern config, got nil")
	}
	var ise *IllegalStateError
	if !errors.As(err, &ise) {
		t.Fatalf("expected *IllegalStateError, got %T: %v", err, err)
	}
}

// testCreateGood: blacklist-all.conf: isProfiling()==false; blacklist first
// pattern == ".*"; whitelist first == "java\\.lang\\..*".
func TestConfiguration_CreateGood(t *testing.T) {
	config, err := NewConfiguration("../testdata/blacklist-all.conf")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config.IsProfiling() {
		t.Fatal("expected profiling to be false")
	}
	blacklist := config.Blacklist().Patterns()
	if len(blacklist) == 0 {
		t.Fatal("expected non-empty blacklist")
	}
	if got := blacklist[0].Pattern(); got != ".*" {
		t.Fatalf("expected blacklist first pattern \".*\", got %q", got)
	}
	whitelist := config.Whitelist().Patterns()
	if len(whitelist) == 0 {
		t.Fatal("expected non-empty whitelist")
	}
	if got := whitelist[0].Pattern(); got != `java\.lang\..*` {
		t.Fatalf("expected whitelist first pattern %q, got %q", `java\.lang\..*`, got)
	}
}

// testReload: copy blacklist-all-refresh-10-ms.conf to temp; Configuration(temp);
// assertFalse profiling; blacklist first ".*", whitelist first "java\\.lang\\..*";
// copy whitelist-all.conf over temp; sleep/touch/sleep; reloadIfNeeded();
// blacklist iterator hasNext()==false (EMPTY), whitelist first == ".*".
func TestConfiguration_Reload(t *testing.T) {
	tmpDir := t.TempDir()
	tmpConf := filepath.Join(tmpDir, "reload.conf")

	if err := copyFile("../testdata/blacklist-all-refresh-10-ms.conf", tmpConf); err != nil {
		t.Fatalf("failed to copy initial config: %v", err)
	}

	config, err := NewConfiguration(tmpConf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config.IsProfiling() {
		t.Fatal("expected profiling to be false")
	}
	if got := config.Blacklist().Patterns()[0].Pattern(); got != ".*" {
		t.Fatalf("expected blacklist first \".*\", got %q", got)
	}
	if got := config.Whitelist().Patterns()[0].Pattern(); got != `java\.lang\..*` {
		t.Fatalf("expected whitelist first %q, got %q", `java\.lang\..*`, got)
	}

	// Overwrite with whitelist-all.conf (empty blacklist, whitelist .*).
	time.Sleep(50 * time.Millisecond)
	if err := copyFile("../testdata/whitelist-all.conf", tmpConf); err != nil {
		t.Fatalf("failed to overwrite config: %v", err)
	}
	touch(t, tmpConf)
	time.Sleep(50 * time.Millisecond)

	config.ReloadIfNeeded()

	if config.Blacklist().Len() != 0 {
		t.Fatalf("expected empty blacklist after reload, got %d patterns", config.Blacklist().Len())
	}
	if got := config.Whitelist().Patterns()[0].Pattern(); got != ".*" {
		t.Fatalf("expected whitelist first \".*\" after reload, got %q", got)
	}
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

func touch(t *testing.T, path string) {
	t.Helper()
	now := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, now, now); err != nil {
		t.Fatalf("failed to touch %s: %v", path, err)
	}
}
