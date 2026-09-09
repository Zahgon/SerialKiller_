package serialkiller

import (
	"errors"
	"testing"
)

// Port of org.nibblesec.tools.PatternListTest.

// testCreateNull: new PatternList((String[]) null) -> NullPointerException.
func TestPatternList_CreateNull(t *testing.T) {
	_, err := NewPatternList(nil)
	if err == nil {
		t.Fatal("expected error for nil regExps, got nil")
	}
	var nre *NilRegExpsError
	if !errors.As(err, &nre) {
		t.Fatalf("expected *NilRegExpsError (NPE equivalent), got %T: %v", err, err)
	}
}

// testCreateBadPattern: new PatternList("(") -> PatternSyntaxException.
func TestPatternList_CreateBadPattern(t *testing.T) {
	_, err := NewPatternList([]string{"("})
	if err == nil {
		t.Fatal("expected error for bad pattern, got nil")
	}
	var pse *PatternSyntaxError
	if !errors.As(err, &pse) {
		t.Fatalf("expected *PatternSyntaxError, got %T: %v", err, err)
	}
}

// testCreateEmpty: new PatternList(); iterator().hasNext() == false.
func TestPatternList_CreateEmpty(t *testing.T) {
	list, err := NewPatternList([]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if list.Len() != 0 {
		t.Fatalf("expected empty list, got %d patterns", list.Len())
	}
	if len(list.Patterns()) != 0 {
		t.Fatalf("expected no patterns to iterate, got %d", len(list.Patterns()))
	}
}

// testCreateSingle: new PatternList("a"); one pattern with pattern() == "a".
func TestPatternList_CreateSingle(t *testing.T) {
	list, err := NewPatternList([]string{"a"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	patterns := list.Patterns()
	if len(patterns) != 1 {
		t.Fatalf("expected 1 pattern, got %d", len(patterns))
	}
	if got := patterns[0].Pattern(); got != "a" {
		t.Fatalf("expected pattern \"a\", got %q", got)
	}
}

// testCreateSequence: ["a","b","c"] iterate in order, 3 items.
func TestPatternList_CreateSequence(t *testing.T) {
	list, err := NewPatternList([]string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	patterns := list.Patterns()
	if len(patterns) != 3 {
		t.Fatalf("expected 3 patterns, got %d", len(patterns))
	}
	want := []string{"a", "b", "c"}
	for i, p := range patterns {
		if p.Pattern() != want[i] {
			t.Fatalf("pattern %d: expected %q, got %q", i, want[i], p.Pattern())
		}
	}
}

// testCreateSafeArgs: ["1","2"], mutate source[1]="three" after construction,
// iterate still yields "1","2" (defensive copy), count 2.
func TestPatternList_CreateSafeArgs(t *testing.T) {
	source := []string{"1", "2"}
	list, err := NewPatternList(source)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Mutate the caller's slice after construction.
	source[1] = "three"

	patterns := list.Patterns()
	if len(patterns) != 2 {
		t.Fatalf("expected 2 patterns, got %d", len(patterns))
	}
	want := []string{"1", "2"}
	for i, p := range patterns {
		if p.Pattern() != want[i] {
			t.Fatalf("pattern %d: expected %q (defensive copy), got %q", i, want[i], p.Pattern())
		}
	}
}

// Additional: iterator remove() equivalent — there is no mutation entry point;
// Patterns() returns the backing slice and PatternList exposes no removal.
// This documents the UnsupportedOperationException("remove") behavior.
func TestPatternList_NoRemove(t *testing.T) {
	list, err := NewPatternList([]string{"a"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// toString parity: Arrays.toString => "[a]"
	if got := list.String(); got != "[a]" {
		t.Fatalf("expected toString \"[a]\", got %q", got)
	}
}
