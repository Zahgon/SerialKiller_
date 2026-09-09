// Package serialkiller is a Go migration of the Java SerialKiller library
// (org.nibblesec.tools.SerialKiller), an easy-to-use look-ahead deserialization
// filter that decides, per class encountered in a Java serialization stream,
// whether deserialization may proceed based on configurable blacklist and
// whitelist regular expressions.
//
// The Java original extends java.io.ObjectInputStream and overrides
// resolveClass. Go has no ObjectInputStream; this package walks the Java
// serialization stream itself (see javaserial.go) and invokes the same
// blacklist/whitelist decision for every class descriptor, in the same order,
// preserving the observable accept/reject behavior, error messages, and logging.
package serialkiller

import (
	"fmt"
	"io"
	"log"
	"sync"
)

// InvalidClassError mirrors java.io.InvalidClassException as thrown by
// SerialKiller.resolveClass. Java's InvalidClassException(String classname,
// String reason) exposes a public `classname` field and a getMessage() of
// "<classname>; <reason>". Both are reproduced so callers can assert on the
// class name and on the message text (which contains "blocked" plus either
// "blacklist" or "whitelist").
type InvalidClassError struct {
	ClassName string
	Reason    string
}

func (e *InvalidClassError) Error() string {
	if e.ClassName != "" {
		return e.ClassName + "; " + e.Reason
	}
	return e.Reason
}

// Logger receives the observable log output produced by resolveClass. It
// mirrors the commons-logging Log used by the Java implementation: Error for
// blocking messages, Info for profiling messages. The default logger writes
// Error to the standard logger and discards Info, matching the default
// commons-logging configuration used in blocking mode (SEVERE level visible).
type Logger interface {
	Error(msg string)
	Info(msg string)
}

type defaultLogger struct{}

func (defaultLogger) Error(msg string) { log.Println(msg) }
func (defaultLogger) Info(msg string)  { _ = msg }

// DefaultLogger is the logger used when none is supplied.
var DefaultLogger Logger = defaultLogger{}

// configCache mirrors the static ConcurrentHashMap<String, Configuration> in
// the Java implementation: Configuration instances are cached by config file
// path and shared across all SerialKiller instances using the same path. This
// per-path isolation is relied upon by the thread/reload behavior.
var (
	configCacheMu sync.Mutex
	configCache   = make(map[string]*Configuration)
)

// getOrCreateConfiguration reproduces
// configs.computeIfAbsent(configFile, Configuration::new).
func getOrCreateConfiguration(configFile string) (*Configuration, error) {
	configCacheMu.Lock()
	defer configCacheMu.Unlock()

	if c, ok := configCache[configFile]; ok {
		return c, nil
	}
	c, err := NewConfiguration(configFile)
	if err != nil {
		return nil, err
	}
	configCache[configFile] = c
	return c, nil
}

// ResetConfigCache clears the shared configuration cache. It exists to support
// tests that need a fresh Configuration for a reused path (the Java tests rely
// on distinct paths instead); production code has no need to call it.
func ResetConfigCache() {
	configCacheMu.Lock()
	defer configCacheMu.Unlock()
	configCache = make(map[string]*Configuration)
}

// SerialKiller is the look-ahead deserialization filter. It mirrors the Java
// class org.nibblesec.tools.SerialKiller, which extends ObjectInputStream.
type SerialKiller struct {
	reader    io.Reader
	config    *Configuration
	profiling bool
	logger    Logger

	started bool
	or      *objectReader
}

// NewSerialKiller constructs a SerialKiller reading from inputStream, using the
// configuration at configFile (loaded via the shared cache). It mirrors
// SerialKiller(InputStream inputStream, String configFile). A configuration
// failure returns an *IllegalStateError (mirroring the IllegalStateException
// the Configuration constructor throws).
func NewSerialKiller(inputStream io.Reader, configFile string) (*SerialKiller, error) {
	config, err := getOrCreateConfiguration(configFile)
	if err != nil {
		return nil, err
	}
	return &SerialKiller{
		reader:    inputStream,
		config:    config,
		profiling: config.IsProfiling(),
		logger:    DefaultLogger,
	}, nil
}

// SetLogger overrides the logger used for observable log output. It must be
// called before the first ReadObject. Passing nil restores the default.
func (sk *SerialKiller) SetLogger(l Logger) {
	if l == nil {
		l = DefaultLogger
	}
	sk.logger = l
}

// resolveClass reproduces SerialKiller.resolveClass exactly:
//  1. reload config if needed;
//  2. blacklist loop (substring/find semantics): on match, either log info and
//     continue (profiling) or log error and reject (blocking);
//  3. whitelist loop: mark safe on first match, log info if profiling, break;
//  4. if not whitelisted and not profiling, log error and reject;
//  5. otherwise accept.
func (sk *SerialKiller) resolveClass(name string) error {
	sk.config.ReloadIfNeeded()

	// Blacklist check.
	for _, blackPattern := range sk.config.Blacklist().Patterns() {
		if blackPattern.Find(name) {
			if sk.profiling {
				sk.logger.Info(fmt.Sprintf("Blacklist match: '%s'", name))
			} else {
				sk.logger.Error(fmt.Sprintf("Blocked by blacklist '%s'. Match found for '%s'", blackPattern.Pattern(), name))
				return &InvalidClassError{
					ClassName: name,
					Reason:    "Class blocked from deserialization (blacklist)",
				}
			}
		}
	}

	// Whitelist check.
	safeClass := false
	for _, whitePattern := range sk.config.Whitelist().Patterns() {
		if whitePattern.Find(name) {
			safeClass = true
			if sk.profiling {
				sk.logger.Info(fmt.Sprintf("Whitelist match: '%s'", name))
			}
			break
		}
	}

	if !safeClass && !sk.profiling {
		sk.logger.Error(fmt.Sprintf("Blocked by whitelist. No match found for '%s'", name))
		return &InvalidClassError{
			ClassName: name,
			Reason:    "Class blocked from deserialization (non-whitelist)",
		}
	}

	return nil
}

// ReadObject reads and returns the next object from the underlying stream,
// applying the resolveClass filter to every class descriptor encountered, in
// stream order. It mirrors ObjectInputStream.readObject() on a SerialKiller
// instance: a filtered class produces an *InvalidClassError and aborts the read
// at exactly that class, matching the Java throw.
//
// For the first call the stream header (magic + version) is validated;
// subsequent calls continue reading further content elements from the same
// stream, mirroring successive readObject() calls.
func (sk *SerialKiller) ReadObject() (interface{}, error) {
	if !sk.started {
		sk.or = newObjectReader(sk.reader, sk.resolveClass)
		sk.started = true
		return sk.or.ReadStream()
	}
	return sk.or.readContent()
}

// Profiling reports whether this SerialKiller is in profiling (non-blocking)
// mode, mirroring the profiling field derived from Configuration#isProfiling().
func (sk *SerialKiller) Profiling() bool {
	return sk.profiling
}

// Config returns the shared Configuration backing this SerialKiller.
func (sk *SerialKiller) Config() *Configuration {
	return sk.config
}
