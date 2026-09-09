package serialkiller

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Port of org.nibblesec.tools.SerialKillerTest.

// testBlacklisted: deserialize hibernate1.ser with serialkiller.conf. Must
// throw InvalidClassException whose message contains "blocked" AND "blacklist",
// NOT "whitelist"; classname == "org.hibernate.engine.spi.TypedValue".
func TestSerialKiller_Blacklisted(t *testing.T) {
	ResetConfigCache()
	data, err := os.ReadFile("../testdata/hibernate1.ser")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}
	sk, err := NewSerialKiller(bytes.NewReader(data), "../testdata/serialkiller.conf")
	if err != nil {
		t.Fatalf("failed to construct SerialKiller: %v", err)
	}
	sk.SetLogger(silentLogger{})

	_, err = sk.ReadObject()
	if err == nil {
		t.Fatal("expected InvalidClassError, got nil")
	}
	var ice *InvalidClassError
	if !errors.As(err, &ice) {
		// A ClassNotFoundException-equivalent (parse error) should fail the
		// test, mirroring the Java test's fail() on ClassNotFoundException.
		t.Fatalf("expected *InvalidClassError, got %T: %v", err, err)
	}
	msg := ice.Error()
	if !strings.Contains(msg, "blocked") {
		t.Fatalf("expected message to contain \"blocked\", got %q", msg)
	}
	if !strings.Contains(msg, "blacklist") {
		t.Fatalf("expected message to contain \"blacklist\", got %q", msg)
	}
	if strings.Contains(msg, "whitelist") {
		t.Fatalf("expected message NOT to contain \"whitelist\", got %q", msg)
	}
	if ice.ClassName != "org.hibernate.engine.spi.TypedValue" {
		t.Fatalf("expected classname org.hibernate.engine.spi.TypedValue, got %q", ice.ClassName)
	}
}

// testNonWhitelisted: deserialize a java.sql.Date(42L) with serialkiller.conf.
// Must throw InvalidClassException message contains "blocked" AND "whitelist"
// NOT "blacklist"; classname == "java.sql.Date".
func TestSerialKiller_NonWhitelisted(t *testing.T) {
	ResetConfigCache()
	data, err := os.ReadFile("../testdata/sqldate.ser")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}
	sk, err := NewSerialKiller(bytes.NewReader(data), "../testdata/serialkiller.conf")
	if err != nil {
		t.Fatalf("failed to construct SerialKiller: %v", err)
	}
	sk.SetLogger(silentLogger{})

	_, err = sk.ReadObject()
	if err == nil {
		t.Fatal("expected InvalidClassError, got nil")
	}
	var ice *InvalidClassError
	if !errors.As(err, &ice) {
		t.Fatalf("expected *InvalidClassError, got %T: %v", err, err)
	}
	msg := ice.Error()
	if !strings.Contains(msg, "blocked") {
		t.Fatalf("expected message to contain \"blocked\", got %q", msg)
	}
	if !strings.Contains(msg, "whitelist") {
		t.Fatalf("expected message to contain \"whitelist\", got %q", msg)
	}
	if strings.Contains(msg, "blacklist") {
		t.Fatalf("expected message NOT to contain \"blacklist\", got %q", msg)
	}
	if ice.ClassName != "java.sql.Date" {
		t.Fatalf("expected classname java.sql.Date, got %q", ice.ClassName)
	}
}

// testWhitelisted: deserialize String "And they all lived happily ever after"
// then int 42 with serialkiller.conf; readObject returns the string then 42.
func TestSerialKiller_Whitelisted(t *testing.T) {
	ResetConfigCache()
	data, err := os.ReadFile("../testdata/string-int.ser")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}
	sk, err := NewSerialKiller(bytes.NewReader(data), "../testdata/serialkiller.conf")
	if err != nil {
		t.Fatalf("failed to construct SerialKiller: %v", err)
	}
	sk.SetLogger(silentLogger{})

	first, err := sk.ReadObject()
	if err != nil {
		t.Fatalf("unexpected error reading string: %v", err)
	}
	if got, ok := first.(string); !ok || got != "And they all lived happily ever after" {
		t.Fatalf("expected string \"And they all lived happily ever after\", got %#v", first)
	}

	second, err := sk.ReadObject()
	if err != nil {
		t.Fatalf("unexpected error reading integer: %v", err)
	}
	if got, ok := second.(int32); !ok || got != 42 {
		t.Fatalf("expected int 42, got %#v", second)
	}
}

// testThreadIssue: serialize 42; open SK w/ blacklist-all.conf; construct a
// second SK w/ whitelist-all.conf; first stream.readObject() must still throw
// InvalidClassException (per-config isolation via cache).
func TestSerialKiller_ThreadIssue(t *testing.T) {
	ResetConfigCache()
	data, err := os.ReadFile("../testdata/int42.ser")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	first, err := NewSerialKiller(bytes.NewReader(data), "../testdata/blacklist-all.conf")
	if err != nil {
		t.Fatalf("failed to construct first SerialKiller: %v", err)
	}
	first.SetLogger(silentLogger{})

	// Construct a second SerialKiller with a different config file.
	second, err := NewSerialKiller(bytes.NewReader(data), "../testdata/whitelist-all.conf")
	if err != nil {
		t.Fatalf("failed to construct second SerialKiller: %v", err)
	}
	second.SetLogger(silentLogger{})

	// The first stream must still reject (blacklist .* blocks java.lang.Integer).
	_, err = first.ReadObject()
	if err == nil {
		t.Fatal("expected InvalidClassError from first stream, got nil")
	}
	var ice *InvalidClassError
	if !errors.As(err, &ice) {
		t.Fatalf("expected *InvalidClassError, got %T: %v", err, err)
	}
}

// testReload: copy blacklist-all-refresh-10-ms.conf to temp; serialize 42;
// open SK on temp; copy whitelist-all.conf over temp; sleep, touch mtime,
// sleep; readObject() must return 42 (hot reload from blacklist-all to
// whitelist-all).
func TestSerialKiller_Reload(t *testing.T) {
	ResetConfigCache()
	tmpDir := t.TempDir()
	tmpConf := filepath.Join(tmpDir, "reload.conf")
	if err := copyFile("../testdata/blacklist-all-refresh-10-ms.conf", tmpConf); err != nil {
		t.Fatalf("failed to copy initial config: %v", err)
	}

	data, err := os.ReadFile("../testdata/int42.ser")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	sk, err := NewSerialKiller(bytes.NewReader(data), tmpConf)
	if err != nil {
		t.Fatalf("failed to construct SerialKiller: %v", err)
	}
	sk.SetLogger(silentLogger{})

	// Overwrite with whitelist-all.conf and force reload.
	time.Sleep(50 * time.Millisecond)
	if err := copyFile("../testdata/whitelist-all.conf", tmpConf); err != nil {
		t.Fatalf("failed to overwrite config: %v", err)
	}
	touch(t, tmpConf)
	time.Sleep(50 * time.Millisecond)

	value, err := sk.ReadObject()
	if err != nil {
		t.Fatalf("expected successful read after reload, got error: %v", err)
	}
	if got, ok := value.(int32); !ok || got != 42 {
		t.Fatalf("expected int 42 after reload, got %#v", value)
	}
}

// silentLogger discards log output during tests (the Java tests do not assert
// on log content; log lines are verified separately against the baseline).
type silentLogger struct{}

func (silentLogger) Error(string) {}
func (silentLogger) Info(string)  {}
