package serialkiller

import (
	"encoding/xml"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// IllegalStateError mirrors java.lang.IllegalStateException thrown by the Java
// Configuration constructor when the configuration cannot be loaded or parsed:
//
//	throw new IllegalStateException("SerialKiller not properly configured: " + e.getMessage(), e)
type IllegalStateError struct {
	Message string
	Cause   error
}

func (e *IllegalStateError) Error() string {
	return e.Message
}

func (e *IllegalStateError) Unwrap() error {
	return e.Cause
}

// xmlConfig is the parsed representation of a serialkiller configuration file.
// It corresponds to the elements read by the Java code via
// commons-configuration XMLConfiguration:
//   - refresh                     -> getLong("refresh", 6000)
//   - mode/profiling              -> getBoolean("mode.profiling", false)
//   - blacklist/regexps/regexp[]  -> getStringArray("blacklist.regexps.regexp")
//   - whitelist/regexps/regexp[]  -> getStringArray("whitelist.regexps.regexp")
//
// The <list><name> block that appears in some configs is intentionally not
// mapped: the Java implementation never reads it (only regexps.regexp).
type xmlConfig struct {
	XMLName   xml.Name    `xml:"config"`
	Refresh   *string     `xml:"refresh"`
	Mode      *xmlMode    `xml:"mode"`
	Blacklist xmlRegexps  `xml:"blacklist"`
	Whitelist xmlRegexps  `xml:"whitelist"`
}

type xmlMode struct {
	Profiling *string `xml:"profiling"`
}

type xmlRegexps struct {
	Regexps struct {
		Regexp []string `xml:"regexp"`
	} `xml:"regexps"`
}

// Configuration mirrors org.nibblesec.tools.SerialKiller.Configuration.
type Configuration struct {
	mu sync.Mutex

	path string

	refresh   int64
	profiling bool
	blacklist *PatternList
	whitelist *PatternList

	// lastModified is the file mtime observed at the last (re)load. Used by
	// reloadIfNeeded together with refresh to reproduce the behavior of
	// commons-configuration FileChangedReloadingStrategy: reload when the file
	// changed on disk and the refresh delay has elapsed since the last check.
	lastModified time.Time
	lastChecked  time.Time
}

// NewConfiguration loads and parses the configuration at configPath, mirroring
// the Java constructor Configuration(final String configPath). Any failure to
// load or parse (missing/empty/non-XML file, or an invalid regex pattern)
// returns an *IllegalStateError, mirroring the IllegalStateException the Java
// constructor throws for ConfigurationException | PatternSyntaxException.
func NewConfiguration(configPath string) (*Configuration, error) {
	c := &Configuration{path: configPath}
	if err := c.load(); err != nil {
		return nil, err
	}
	return c, nil
}

// load reads and parses the config file and (re)initializes the pattern lists.
// Errors are wrapped as *IllegalStateError to mirror the Java constructor.
func (c *Configuration) load() error {
	// Mirror commons-configuration: constructing XMLConfiguration with a null
	// or non-existent/unreadable path fails with ConfigurationException, which
	// the Java constructor rethrows as IllegalStateException.
	if c.path == "" {
		return c.illegalState(fmt.Errorf("null configuration path"))
	}

	info, statErr := os.Stat(c.path)
	if statErr != nil {
		return c.illegalState(statErr)
	}

	data, err := os.ReadFile(c.path)
	if err != nil {
		return c.illegalState(err)
	}

	// An empty file is not valid XML; commons-configuration reports
	// "Premature end of file." and throws ConfigurationException.
	if len(strings.TrimSpace(string(data))) == 0 {
		return c.illegalState(fmt.Errorf("premature end of file"))
	}

	var parsed xmlConfig
	if err := xml.Unmarshal(data, &parsed); err != nil {
		return c.illegalState(err)
	}

	// refresh: getLong("refresh", 6000)
	refresh := int64(6000)
	if parsed.Refresh != nil {
		v, perr := strconv.ParseInt(strings.TrimSpace(*parsed.Refresh), 10, 64)
		if perr != nil {
			return c.illegalState(perr)
		}
		refresh = v
	}

	// mode.profiling: getBoolean("mode.profiling", false)
	profiling := false
	if parsed.Mode != nil && parsed.Mode.Profiling != nil {
		profiling = parseBool(*parsed.Mode.Profiling)
	}

	// init(config): compile pattern lists. A PatternSyntaxException here is
	// rethrown by the Java constructor as IllegalStateException.
	//
	// getStringArray("...regexp") in commons-configuration returns an empty
	// (non-null) array for a missing/empty element, so an empty configuration
	// yields an empty PatternList rather than the null-array NPE case.
	blacklist, err := NewPatternList(emptyIfNil(parsed.Blacklist.Regexps.Regexp))
	if err != nil {
		return c.illegalState(err)
	}
	whitelist, err := NewPatternList(emptyIfNil(parsed.Whitelist.Regexps.Regexp))
	if err != nil {
		return c.illegalState(err)
	}

	c.refresh = refresh
	c.profiling = profiling
	c.blacklist = blacklist
	c.whitelist = whitelist
	c.lastModified = info.ModTime()
	c.lastChecked = time.Now()

	return nil
}

// parseBool mirrors commons-configuration/commons-lang boolean parsing used by
// getBoolean: "true" (case-insensitive) is true, everything else is false.
func parseBool(s string) bool {
	return strings.EqualFold(strings.TrimSpace(s), "true")
}

func emptyIfNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (c *Configuration) illegalState(cause error) error {
	msg := "SerialKiller not properly configured: "
	if cause != nil {
		msg += cause.Error()
	}
	return &IllegalStateError{Message: msg, Cause: cause}
}

// ReloadIfNeeded mirrors Configuration#reloadIfNeeded(), which calls
// config.reload(). With the FileChangedReloadingStrategy, reloading re-reads
// the file (and re-initializes the pattern lists) when the file has changed on
// disk since the last load and the refresh delay has elapsed since the last
// check. Reload failures are swallowed here (as commons-configuration logs and
// keeps the previous configuration) — the previous lists remain in effect.
func (c *Configuration) ReloadIfNeeded() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	// Honour the refresh delay between checks (FileChangedReloadingStrategy
	// setRefreshDelay). refresh is in milliseconds.
	if now.Sub(c.lastChecked) < time.Duration(c.refresh)*time.Millisecond {
		return
	}
	c.lastChecked = now

	info, err := os.Stat(c.path)
	if err != nil {
		// File temporarily unavailable: keep previous configuration.
		return
	}
	if !info.ModTime().After(c.lastModified) {
		return
	}

	// File changed: attempt reload. On failure keep the previous config.
	_ = c.reloadLocked()
}

// reloadLocked re-reads and re-parses the file, updating fields in place. The
// caller must hold c.mu.
func (c *Configuration) reloadLocked() error {
	data, err := os.ReadFile(c.path)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return fmt.Errorf("premature end of file")
	}
	var parsed xmlConfig
	if err := xml.Unmarshal(data, &parsed); err != nil {
		return err
	}

	refresh := int64(6000)
	if parsed.Refresh != nil {
		if v, perr := strconv.ParseInt(strings.TrimSpace(*parsed.Refresh), 10, 64); perr == nil {
			refresh = v
		}
	}
	profiling := false
	if parsed.Mode != nil && parsed.Mode.Profiling != nil {
		profiling = parseBool(*parsed.Mode.Profiling)
	}
	blacklist, err := NewPatternList(emptyIfNil(parsed.Blacklist.Regexps.Regexp))
	if err != nil {
		return err
	}
	whitelist, err := NewPatternList(emptyIfNil(parsed.Whitelist.Regexps.Regexp))
	if err != nil {
		return err
	}

	info, err := os.Stat(c.path)
	if err != nil {
		return err
	}

	c.refresh = refresh
	c.profiling = profiling
	c.blacklist = blacklist
	c.whitelist = whitelist
	c.lastModified = info.ModTime()
	return nil
}

// Blacklist returns the current blacklist patterns, mirroring
// Configuration#blacklist().
func (c *Configuration) Blacklist() *PatternList {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.blacklist
}

// Whitelist returns the current whitelist patterns, mirroring
// Configuration#whitelist().
func (c *Configuration) Whitelist() *PatternList {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.whitelist
}

// IsProfiling mirrors Configuration#isProfiling().
func (c *Configuration) IsProfiling() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.profiling
}
