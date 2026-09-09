# SerialKiller (Go)

SerialKiller is an easy-to-use look-ahead deserialization filter for Java
serialization streams. It decides, for every class encountered while decoding a
Java serialized object graph, whether deserialization may proceed, based on
configurable **blacklist** and **whitelist** regular expressions.

This is a faithful Go migration of the original Java library
[`org.nibblesec.tools.SerialKiller`](https://github.com/ikkisoft/SerialKiller).
It preserves the original's observable behavior: the same filtering rules and
precedence, the same accept/reject decisions, the same error messages, and the
same log output.

## How the migration relates to the Java original

The Java `SerialKiller` extends `java.io.ObjectInputStream` and overrides
`resolveClass(...)` to apply the blacklist/whitelist decision for each class the
JVM is about to resolve while deserializing.

Go has no `ObjectInputStream`. To reproduce the same behavior, this package
includes a parser for the Java serialization stream format (see
`serialkiller/javaserial.go`). It walks the stream, discovers every class
descriptor in the exact order Java's `ObjectInputStream` would resolve them
(including superclass descriptors and array element classes), and invokes the
same blacklist/whitelist decision for each. When a class is rejected, the read
aborts at exactly that class — mirroring the `InvalidClassException` the JVM
would throw mid-stream.

The security guarantees of the original are preserved: the filter is
**default-deny** (a class must match the whitelist to be accepted), the
blacklist is checked first, and rejection aborts the whole deserialization.

## Requirements

- Go 1.23 or newer.
- No third-party dependencies. The implementation uses only the Go standard
  library (`encoding/xml`, `regexp`, `os`, `sync`, `log`, `encoding/binary`,
  etc.).

The Java original depended on `commons-configuration` (XML config parsing +
file-change reloading), `commons-logging` (log output), and pulled in
`commons-collections`/`commons-lang` transitively. Their behaviorally relevant
parts are reproduced here directly with the standard library: XML config parsing
via `encoding/xml`, refresh-based file reloading via file mtime checks, and log
output via the `log` package behind a small `Logger` interface.

## Build and test

```sh
go build ./...
go test ./...
```

To see the speed-test timing output:

```sh
go test -v -run TestSerialKiller_Speed ./serialkiller/
```

## Usage

`NewSerialKiller` wraps any `io.Reader` carrying a Java serialization stream and
filters it against a configuration file. `ReadObject` decodes the next object,
applying the filter to every class in the graph.

```go
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/ikkisoft/serialkiller/serialkiller"
)

func main() {
	f, err := os.Open("payload.ser")
	if err != nil {
		panic(err)
	}
	defer f.Close()

	sk, err := serialkiller.NewSerialKiller(f, "config/serialkiller.conf")
	if err != nil {
		// Configuration could not be loaded/parsed.
		panic(err)
	}

	obj, err := sk.ReadObject()
	if err != nil {
		var ice *serialkiller.InvalidClassError
		if errors.As(err, &ice) {
			// A class was blocked by the blacklist/whitelist filter.
			fmt.Printf("blocked class %q: %s\n", ice.ClassName, ice.Error())
			return
		}
		panic(err)
	}

	fmt.Printf("deserialized: %v\n", obj)
}
```

## Configuration

The configuration file is XML, identical in shape to the Java original. Example:

```xml
<config>
  <refresh>6000</refresh>
  <mode>
    <profiling>false</profiling>
  </mode>
  <blacklist>
    <regexps>
      <regexp>bsh\.XThis$</regexp>
      <regexp>org\.hibernate\.engine\.spi\.TypedValue$</regexp>
      <!-- ... -->
    </regexps>
  </blacklist>
  <whitelist>
    <regexps>
      <regexp>java\.lang\..*</regexp>
      <regexp>java\.util\..*</regexp>
    </regexps>
  </whitelist>
</config>
```

Elements read by the implementation:

| Element                      | Meaning                                   | Default |
|------------------------------|-------------------------------------------|---------|
| `refresh`                    | Reload check interval, in milliseconds    | `6000`  |
| `mode/profiling`             | Profiling (non-blocking) mode when `true` | `false` |
| `blacklist/regexps/regexp`   | Ordered list of blacklist patterns        | (empty) |
| `whitelist/regexps/regexp`   | Ordered list of whitelist patterns        | (empty) |

Notes:

- A `<list><name>...</name></list>` block appears in some historical configs.
  It is **ignored** — the original implementation never read it, and neither
  does this migration.
- An empty `<blacklist></blacklist>` (or `<whitelist></whitelist>`) yields an
  empty pattern list.
- A missing/empty/non-XML file, or a regex pattern that fails to compile,
  causes configuration loading to fail with an `*IllegalStateError` (mirroring
  the Java `IllegalStateException`).

Configuration instances are cached and shared by file path (mirroring the
static cache in the Java original), so multiple `SerialKiller` instances built
from the same config file share one `Configuration`.

### Hot reload

When the config file changes on disk, it is reloaded on the next filtering
decision, provided the `refresh` interval has elapsed since the last check —
reproducing the Java `FileChangedReloadingStrategy` with `setRefreshDelay`.
Reload failures are swallowed and the previous configuration remains in effect.

## Filtering and security semantics

For each class name encountered in the stream, `resolveClass` applies exactly
this logic (identical to the Java original):

1. **Blacklist** (checked first). Each blacklist pattern is tested against the
   class name using **unanchored substring matching** (Java `Matcher.find()`;
   Go `regexp.MatchString` has the same semantics).
   - In blocking mode: on the first blacklist match, the class is **rejected**
     with an `*InvalidClassError` whose reason is
     `Class blocked from deserialization (blacklist)`, and an error line
     `Blocked by blacklist '<pattern>'. Match found for '<class>'` is logged.
   - In profiling mode: a match is logged (`Blacklist match: '<class>'`) but the
     class is **not** rejected.
2. **Whitelist**. Each whitelist pattern is tested with the same substring
   semantics. The first match marks the class safe.
   - In blocking mode: if **no** whitelist pattern matches, the class is
     **rejected** with reason
     `Class blocked from deserialization (non-whitelist)`, and an error line
     `Blocked by whitelist. No match found for '<class>'` is logged.
   - In profiling mode: a class is never rejected.
3. Otherwise the class is **accepted**.

Key properties (preserved from the original):

- **Default deny**: a class that matches nothing is rejected.
- **Blacklist precedence**: a class matching both lists is rejected by the
  blacklist.
- **Fail closed / abort**: the first rejected class aborts the entire
  deserialization at that point in the stream.
- Rejection applies to every class in the object graph, including nested
  objects, superclass descriptors, and array element types.

## Public API

Package `github.com/ikkisoft/serialkiller/serialkiller`.

| Java construct                                   | Go equivalent                                                  |
|--------------------------------------------------|----------------------------------------------------------------|
| `SerialKiller(InputStream, String)`              | `NewSerialKiller(io.Reader, string) (*SerialKiller, error)`    |
| `SerialKiller.readObject()`                      | `(*SerialKiller).ReadObject() (interface{}, error)`            |
| `resolveClass` decision / `isProfiling`          | `(*SerialKiller).Profiling() bool`, `Config() *Configuration`  |
| `SerialKiller.Configuration`                     | `Configuration`, `NewConfiguration(string) (*Configuration, error)` |
| `Configuration.reloadIfNeeded()`                 | `(*Configuration).ReloadIfNeeded()`                            |
| `Configuration.blacklist()/whitelist()`          | `(*Configuration).Blacklist()/Whitelist() *PatternList`        |
| `Configuration.isProfiling()`                    | `(*Configuration).IsProfiling() bool`                          |
| `SerialKiller.PatternList`                       | `PatternList`, `NewPatternList([]string) (*PatternList, error)`|
| `PatternList` iteration / `toString()`           | `(*PatternList).Patterns() []*Pattern`, `Len()`, `String()`    |
| `java.util.regex.Pattern#pattern()`              | `(*Pattern).Pattern() string`, `Find(string) bool`, `String()` |
| `InvalidClassException(classname, reason)`       | `*InvalidClassError{ClassName, Reason}`                        |
| `IllegalStateException`                          | `*IllegalStateError`                                           |
| `NullPointerException` (`requireNonNull`)        | `*NilRegExpsError`                                             |
| `PatternSyntaxException`                         | `*PatternSyntaxError`                                          |
| `commons-logging` Log                            | `Logger` interface + `DefaultLogger`; `SetLogger`              |

`InvalidClassError` exposes the offending `ClassName` and an `Error()` of
`"<classname>; <reason>"`, matching Java's
`InvalidClassException.getMessage()`.

The `Logger` interface lets callers capture the observable log output
(`Error`/`Info`). The default logger writes `Error` lines to the standard
logger and discards `Info`, matching the default blocking-mode logging of the
original.

## Tests

The Go test suite ports every scenario from the original Java test suite
(`ConfigurationTest`, `PatternListTest`, `SerialKillerTest`,
`SerialKillerSpeedTest`) and adds explicit security-boundary tests. Run:

```sh
go test ./...
```

The serialized fixtures under `testdata/` (`*.ser`) are byte-for-byte Java
serialization streams produced by a JVM `ObjectOutputStream`, so the tests
exercise the real stream format the filter must handle — including the
`hibernate1.ser` gadget payload used to verify blacklist rejection.

## License

See [LICENSE](LICENSE). This project retains the original SerialKiller license.
