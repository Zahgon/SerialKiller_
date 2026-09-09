package serialkiller

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
)

// Additional security-boundary tests. These are additive and do not replace any
// ported Java test; they pin down observable behavior that the original test
// suite exercised only implicitly (exact log lines, blacklist precedence over
// whitelist, default-deny for unmatched classes, and mid-graph rejection).

// capturingLogger records the exact Error/Info lines emitted by resolveClass so
// tests can assert on the observable log output verified against the Java
// baseline.
type capturingLogger struct {
	errors []string
	infos  []string
}

func (c *capturingLogger) Error(msg string) { c.errors = append(c.errors, msg) }
func (c *capturingLogger) Info(msg string)  { c.infos = append(c.infos, msg) }

// TestSecurity_BlacklistLogLine asserts the exact SEVERE log line emitted when
// a blacklisted class is blocked, matching the Java baseline output:
// "Blocked by blacklist 'org\.hibernate\.engine\.spi\.TypedValue$'. Match found for 'org.hibernate.engine.spi.TypedValue'".
func TestSecurity_BlacklistLogLine(t *testing.T) {
	ResetConfigCache()
	data, err := os.ReadFile("../testdata/hibernate1.ser")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}
	sk, err := NewSerialKiller(bytes.NewReader(data), "../testdata/serialkiller.conf")
	if err != nil {
		t.Fatalf("failed to construct SerialKiller: %v", err)
	}
	logger := &capturingLogger{}
	sk.SetLogger(logger)

	if _, err = sk.ReadObject(); err == nil {
		t.Fatal("expected InvalidClassError, got nil")
	}

	want := "Blocked by blacklist 'org\\.hibernate\\.engine\\.spi\\.TypedValue$'. Match found for 'org.hibernate.engine.spi.TypedValue'"
	if len(logger.errors) != 1 || logger.errors[0] != want {
		t.Fatalf("expected exactly one error line %q, got %#v", want, logger.errors)
	}
	if len(logger.infos) != 0 {
		t.Fatalf("expected no info lines in blocking mode, got %#v", logger.infos)
	}
}

// TestSecurity_WhitelistLogLine asserts the exact SEVERE log line emitted when a
// non-whitelisted class is blocked, matching the Java baseline output:
// "Blocked by whitelist. No match found for 'java.sql.Date'".
func TestSecurity_WhitelistLogLine(t *testing.T) {
	ResetConfigCache()
	data, err := os.ReadFile("../testdata/sqldate.ser")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}
	sk, err := NewSerialKiller(bytes.NewReader(data), "../testdata/serialkiller.conf")
	if err != nil {
		t.Fatalf("failed to construct SerialKiller: %v", err)
	}
	logger := &capturingLogger{}
	sk.SetLogger(logger)

	if _, err = sk.ReadObject(); err == nil {
		t.Fatal("expected InvalidClassError, got nil")
	}

	want := "Blocked by whitelist. No match found for 'java.sql.Date'"
	if len(logger.errors) != 1 || logger.errors[0] != want {
		t.Fatalf("expected exactly one error line %q, got %#v", want, logger.errors)
	}
}

// TestSecurity_BlacklistPrecedesWhitelist verifies that a class matching BOTH
// lists is rejected by the blacklist (the blacklist loop runs first and throws
// before the whitelist is ever consulted). blacklist-all.conf blacklists ".*"
// and whitelists "java\.lang\..*"; java.lang.Integer matches both, and the
// rejection must come from the blacklist.
func TestSecurity_BlacklistPrecedesWhitelist(t *testing.T) {
	ResetConfigCache()
	data, err := os.ReadFile("../testdata/int42.ser")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}
	sk, err := NewSerialKiller(bytes.NewReader(data), "../testdata/blacklist-all.conf")
	if err != nil {
		t.Fatalf("failed to construct SerialKiller: %v", err)
	}
	sk.SetLogger(silentLogger{})

	_, err = sk.ReadObject()
	var ice *InvalidClassError
	if !errors.As(err, &ice) {
		t.Fatalf("expected *InvalidClassError, got %T: %v", err, err)
	}
	if !strings.Contains(ice.Error(), "blacklist") || strings.Contains(ice.Error(), "non-whitelist") {
		t.Fatalf("expected rejection to come from blacklist, got %q", ice.Error())
	}
	if ice.ClassName != "java.lang.Integer" {
		t.Fatalf("expected classname java.lang.Integer, got %q", ice.ClassName)
	}
}

// TestSecurity_FirstBlacklistedClassAbortsGraph verifies that deserialization is
// aborted at exactly the first blacklisted class in a nested object graph. In
// hibernate1.ser the outer java.util.HashMap passes both lists (whitelisted by
// java\.util\..*, not blacklisted), and the nested
// org.hibernate.engine.spi.TypedValue is the class that triggers rejection.
func TestSecurity_FirstBlacklistedClassAbortsGraph(t *testing.T) {
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
	var ice *InvalidClassError
	if !errors.As(err, &ice) {
		t.Fatalf("expected *InvalidClassError, got %T: %v", err, err)
	}
	if ice.ClassName != "org.hibernate.engine.spi.TypedValue" {
		t.Fatalf("expected rejection at org.hibernate.engine.spi.TypedValue, got %q", ice.ClassName)
	}
}

// TestSecurity_ProfilingModeDoesNotBlock verifies that in profiling mode a
// blacklist match is logged but not blocked, and an otherwise non-whitelisted
// class is not blocked either (mode.profiling = true disables all rejections).
func TestSecurity_ProfilingModeDoesNotBlock(t *testing.T) {
	logger := &capturingLogger{}
	cfg := &Configuration{}
	sk := &SerialKiller{config: cfg, profiling: true, logger: logger}

	bl, err := NewPatternList([]string{".*"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wl, err := NewPatternList([]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg.blacklist = bl
	cfg.whitelist = wl
	cfg.refresh = 6000

	if err := sk.resolveClass("com.example.Anything"); err != nil {
		t.Fatalf("profiling mode must not block, got %v", err)
	}
	if len(logger.errors) != 0 {
		t.Fatalf("profiling mode must not emit error lines, got %#v", logger.errors)
	}
	if len(logger.infos) == 0 {
		t.Fatal("profiling mode should log an info line on blacklist match")
	}
}
