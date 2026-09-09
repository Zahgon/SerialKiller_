package serialkiller

import (
	"fmt"
	"regexp"
	"strings"
)

// Pattern is a compiled regular expression paired with its original source
// string. It mirrors java.util.regex.Pattern to the extent SerialKiller relies
// on it: matching (via find/substring semantics) and Pattern#pattern() which
// returns the original source string.
type Pattern struct {
	re  *regexp.Regexp
	src string
}

// Pattern returns the original regular-expression source string, mirroring
// java.util.regex.Pattern#pattern().
func (p *Pattern) Pattern() string {
	return p.src
}

// Find reports whether the pattern matches anywhere within the input, mirroring
// java.util.regex.Matcher#find() (an unanchored substring search). Go's
// regexp.MatchString has the same unanchored semantics.
func (p *Pattern) Find(input string) bool {
	return p.re.MatchString(input)
}

// String mirrors Pattern#toString(), which returns the source string.
func (p *Pattern) String() string {
	return p.src
}

// NilRegExpsError mirrors the NullPointerException thrown by
// Objects.requireNonNull(regExps, "regExps") in the Java PatternList
// constructor when the varargs array is null.
type NilRegExpsError struct {
	Name string
}

func (e *NilRegExpsError) Error() string {
	return e.Name
}

// PatternSyntaxError mirrors java.util.regex.PatternSyntaxException: a regular
// expression could not be compiled.
type PatternSyntaxError struct {
	Pattern string
	Err     error
}

func (e *PatternSyntaxError) Error() string {
	return fmt.Sprintf("invalid pattern %q: %v", e.Pattern, e.Err)
}

func (e *PatternSyntaxError) Unwrap() error {
	return e.Err
}

// PatternList is an immutable, ordered list of compiled patterns. It mirrors
// org.nibblesec.tools.SerialKiller.PatternList (implements Iterable<Pattern>).
type PatternList struct {
	patterns []*Pattern
}

// NewPatternList compiles the given regular-expression source strings into a
// PatternList, mirroring the Java constructor
// PatternList(final String... regExps).
//
// The Java constructor first calls Objects.requireNonNull(regExps, "regExps");
// passing a null array throws NullPointerException. In Go, a nil slice is
// distinguished from an empty (non-nil) slice via the nilGuard flag: callers
// that must reproduce the "null array" case pass a nil slice, which returns a
// *NilRegExpsError. An empty (but non-nil) slice compiles to an empty list,
// mirroring new PatternList() (zero varargs -> empty non-null array).
//
// Any element that fails to compile produces a *PatternSyntaxError, mirroring
// the PatternSyntaxException thrown by Pattern.compile.
//
// The input slice is defensively copied so later mutation of the caller's slice
// does not affect the constructed list (mirroring the Java defensive copy).
func NewPatternList(regExps []string) (*PatternList, error) {
	if regExps == nil {
		return nil, &NilRegExpsError{Name: "regExps"}
	}

	// Defensive copy of the source strings before compiling, mirroring the
	// Java constructor copying into its own array.
	copied := make([]string, len(regExps))
	copy(copied, regExps)

	patterns := make([]*Pattern, 0, len(copied))
	for _, src := range copied {
		re, err := regexp.Compile(src)
		if err != nil {
			return nil, &PatternSyntaxError{Pattern: src, Err: err}
		}
		patterns = append(patterns, &Pattern{re: re, src: src})
	}

	return &PatternList{patterns: patterns}, nil
}

// Patterns returns the ordered patterns. Callers must not mutate the returned
// slice; it backs the list. Iteration order matches insertion order, mirroring
// the Java Iterable contract (and the custom iterator whose remove() throws
// UnsupportedOperationException — there is no mutation entry point here).
func (l *PatternList) Patterns() []*Pattern {
	return l.patterns
}

// Len returns the number of patterns.
func (l *PatternList) Len() int {
	return len(l.patterns)
}

// String mirrors PatternList#toString(), which returns Arrays.toString(patterns)
// — a bracketed, comma-space-separated list of the pattern source strings.
func (l *PatternList) String() string {
	parts := make([]string, len(l.patterns))
	for i, p := range l.patterns {
		parts[i] = p.String()
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
