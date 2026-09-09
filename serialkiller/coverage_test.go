package serialkiller

// Additive unit tests that exercise the lower-level branches of the Java
// serialization stream parser (javaserial.go) and a handful of trivial
// accessors and error types. These tests construct minimal, well-formed
// java.io.ObjectOutputStream-compatible byte streams in memory and feed them
// through the same objectReader used by SerialKiller.ReadObject, so the parser
// walks each production exactly as it would for a real stream. They do not
// alter any filtering or security behavior; they only add coverage for code
// paths that the higher-level fixture tests do not reach.

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

// streamBuilder assembles a Java serialization byte stream. Every helper writes
// raw bytes in the exact big-endian layout produced by ObjectOutputStream.
type streamBuilder struct {
	buf bytes.Buffer
}

func newStreamBuilder() *streamBuilder {
	b := &streamBuilder{}
	// STREAM_MAGIC + STREAM_VERSION.
	b.u16(streamMagic)
	b.u16(streamVersion)
	return b
}

func (b *streamBuilder) byte(v byte)     { b.buf.WriteByte(v) }
func (b *streamBuilder) raw(p ...byte)   { b.buf.Write(p) }
func (b *streamBuilder) u16(v uint16)    { _ = binary.Write(&b.buf, binary.BigEndian, v) }
func (b *streamBuilder) u32(v uint32)    { _ = binary.Write(&b.buf, binary.BigEndian, v) }
func (b *streamBuilder) i64(v int64)     { _ = binary.Write(&b.buf, binary.BigEndian, v) }
func (b *streamBuilder) utf(s string) {
	b.u16(uint16(len(s)))
	b.buf.WriteString(s)
}

func (b *streamBuilder) bytes() []byte { return b.buf.Bytes() }

// classDescShort writes a minimal TC_CLASSDESC: name, zero serialVersionUID,
// the given flags, and zero fields, followed by an empty class annotation and a
// TC_NULL superclass descriptor.
func (b *streamBuilder) classDescShort(name string, flags byte) {
	b.byte(tcClassDesc)
	b.utf(name)
	b.i64(0)             // serialVersionUID
	b.byte(flags)        // classDescFlags
	b.u16(0)             // field count
	b.byte(tcEndBlockData) // end class annotation
	b.byte(tcNull)       // super classDesc
}

// reader builds an objectReader over the assembled stream with an accept-all
// filter, positioned just after the stream header (header consumed by
// ReadStream in the tests that call it, or skipped here for direct content).
func readerForContent(t *testing.T, payload []byte) *objectReader {
	t.Helper()
	// Skip the 4-byte header; these helpers exercise readContent directly.
	return newObjectReader(bytes.NewReader(payload[4:]), func(string) error { return nil })
}

func TestCoverage_ReadStreamBadHeader(t *testing.T) {
	or := newObjectReader(bytes.NewReader([]byte{0x00, 0x01, 0x00, 0x05}), nil)
	if _, err := or.ReadStream(); err == nil {
		t.Fatal("expected error for invalid stream header, got nil")
	}
}

func TestCoverage_ReadStreamGoodHeaderThenNull(t *testing.T) {
	b := newStreamBuilder()
	b.byte(tcNull)
	or := newObjectReader(bytes.NewReader(b.bytes()), nil)
	v, err := or.ReadStream()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != nil {
		t.Fatalf("expected nil for TC_NULL, got %v", v)
	}
}

func TestCoverage_LongString(t *testing.T) {
	var b bytes.Buffer
	b.WriteByte(tcLongString)
	const s = "long string value"
	_ = binary.Write(&b, binary.BigEndian, int64(len(s)))
	b.WriteString(s)
	or := newObjectReader(bytes.NewReader(b.Bytes()), nil)
	v, err := or.readContent()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != s {
		t.Fatalf("expected %q, got %v", s, v)
	}
}

func TestCoverage_BlockDataAndBlockDataLong(t *testing.T) {
	// TC_BLOCKDATA: 1-byte length then payload.
	var short bytes.Buffer
	short.WriteByte(tcBlockData)
	short.WriteByte(0x03)
	short.Write([]byte{1, 2, 3})
	or := newObjectReader(bytes.NewReader(short.Bytes()), nil)
	if _, err := or.readContent(); err != nil {
		t.Fatalf("block data: unexpected error: %v", err)
	}

	// TC_BLOCKDATALONG: 4-byte length then payload.
	var long bytes.Buffer
	long.WriteByte(tcBlockDataLong)
	_ = binary.Write(&long, binary.BigEndian, uint32(4))
	long.Write([]byte{9, 8, 7, 6})
	or2 := newObjectReader(bytes.NewReader(long.Bytes()), nil)
	if _, err := or2.readContent(); err != nil {
		t.Fatalf("block data long: unexpected error: %v", err)
	}
}

func TestCoverage_ResetTag(t *testing.T) {
	// TC_RESET clears the handle table, then reads the following content.
	var b bytes.Buffer
	b.WriteByte(tcReset)
	b.WriteByte(tcNull)
	or := newObjectReader(bytes.NewReader(b.Bytes()), nil)
	if _, err := or.readContent(); err != nil {
		t.Fatalf("reset: unexpected error: %v", err)
	}
}

func TestCoverage_ExceptionTag(t *testing.T) {
	or := newObjectReader(bytes.NewReader([]byte{tcException}), nil)
	if _, err := or.readContent(); err == nil {
		t.Fatal("expected error for TC_EXCEPTION, got nil")
	}
}

func TestCoverage_UnexpectedTypeCode(t *testing.T) {
	or := newObjectReader(bytes.NewReader([]byte{0x00}), nil)
	if _, err := or.readContent(); err == nil {
		t.Fatal("expected error for unexpected type code, got nil")
	}
}

func TestCoverage_Enum(t *testing.T) {
	b := newStreamBuilder()
	b.byte(tcEnum)
	b.classDescShort("com.example.Color", scSerializable|scEnum)
	// enum constant name as a TC_STRING.
	b.byte(tcString)
	b.utf("RED")
	or := readerForContent(t, b.bytes())
	v, err := or.readContent()
	if err != nil {
		t.Fatalf("enum: unexpected error: %v", err)
	}
	if v != "RED" {
		t.Fatalf("expected enum constant RED, got %v", v)
	}
}

func TestCoverage_ArrayPrimitiveByte(t *testing.T) {
	b := newStreamBuilder()
	b.byte(tcArray)
	b.classDescShort("[B", scSerializable)
	b.u32(3) // array length (readI32)
	b.raw(0x0A, 0x0B, 0x0C)
	or := readerForContent(t, b.bytes())
	if _, err := or.readContent(); err != nil {
		t.Fatalf("byte array: unexpected error: %v", err)
	}
}

func TestCoverage_ArrayPrimitiveInt(t *testing.T) {
	b := newStreamBuilder()
	b.byte(tcArray)
	b.classDescShort("[I", scSerializable)
	b.u32(2)
	b.u32(1)
	b.u32(2)
	or := readerForContent(t, b.bytes())
	if _, err := or.readContent(); err != nil {
		t.Fatalf("int array: unexpected error: %v", err)
	}
}

func TestCoverage_ArrayPrimitiveLong(t *testing.T) {
	b := newStreamBuilder()
	b.byte(tcArray)
	b.classDescShort("[J", scSerializable)
	b.u32(1)
	b.i64(42)
	or := readerForContent(t, b.bytes())
	if _, err := or.readContent(); err != nil {
		t.Fatalf("long array: unexpected error: %v", err)
	}
}

func TestCoverage_ArrayPrimitiveCharShort(t *testing.T) {
	b := newStreamBuilder()
	b.byte(tcArray)
	b.classDescShort("[C", scSerializable)
	b.u32(2)
	b.u16(0x0041) // 'A'
	b.u16(0x0042) // 'B'
	or := readerForContent(t, b.bytes())
	if _, err := or.readContent(); err != nil {
		t.Fatalf("char array: unexpected error: %v", err)
	}
}

func TestCoverage_ArrayOfObjects(t *testing.T) {
	b := newStreamBuilder()
	b.byte(tcArray)
	b.classDescShort("[Ljava.lang.Object;", scSerializable)
	b.u32(2)
	b.byte(tcNull)   // element 0
	b.byte(tcString) // element 1
	b.utf("hello")
	or := readerForContent(t, b.bytes())
	if _, err := or.readContent(); err != nil {
		t.Fatalf("object array: unexpected error: %v", err)
	}
}

func TestCoverage_ArrayUnknownElementType(t *testing.T) {
	b := newStreamBuilder()
	b.byte(tcArray)
	// Name with an unsupported element type character in position 1.
	b.classDescShort("[?", scSerializable)
	b.u32(1)
	b.byte(0x00)
	or := readerForContent(t, b.bytes())
	if _, err := or.readContent(); err == nil {
		t.Fatal("expected error for unknown array element type, got nil")
	}
}

// TestCoverage_ObjectAllPrimitiveFields drives readFields across every primitive
// typecode (B, C, D, F, I, J, S, Z) in a single class descriptor.
func TestCoverage_ObjectAllPrimitiveFields(t *testing.T) {
	b := newStreamBuilder()
	b.byte(tcObject)
	b.byte(tcClassDesc)
	b.utf("com.example.AllPrims")
	b.i64(0)
	b.byte(scSerializable)
	b.u16(8) // eight primitive fields
	fields := []struct {
		tc   byte
		name string
	}{
		{'B', "b"}, {'C', "c"}, {'D', "d"}, {'F', "f"},
		{'I', "i"}, {'J', "j"}, {'S', "s"}, {'Z', "z"},
	}
	for _, f := range fields {
		b.byte(f.tc)
		b.utf(f.name)
	}
	b.byte(tcEndBlockData) // end class annotation
	b.byte(tcNull)         // super classDesc
	// Field values in declaration order.
	b.byte(0x01)                                     // B
	b.u16(0x0041)                                    // C
	b.i64(0x4045000000000000)                        // D (double bits, unread)
	b.u32(0x42280000)                                // F (float bits, unread)
	b.u32(7)                                         // I
	b.i64(9)                                         // J
	b.u16(0x0003)                                    // S
	b.byte(0x01)                                     // Z
	or := readerForContent(t, b.bytes())
	if _, err := or.readContent(); err != nil {
		t.Fatalf("all-primitive fields: unexpected error: %v", err)
	}
}

// TestCoverage_ObjectWithReferenceCycle covers getHandle / TC_REFERENCE: the
// second object field references the first object's handle.
func TestCoverage_ObjectWithReferenceHandle(t *testing.T) {
	b := newStreamBuilder()
	b.byte(tcObject)
	b.byte(tcClassDesc)
	b.utf("com.example.Node")
	b.i64(0)
	b.byte(scSerializable)
	b.u16(1) // one object field "self"
	b.byte('L')
	b.utf("self")
	// field type class-name string (TC_STRING).
	b.byte(tcString)
	b.utf("Lcom/example/Node;")
	b.byte(tcEndBlockData)
	b.byte(tcNull)
	// Field value: a reference to the first newly-created handle. The object
	// handle is assigned right after readClassDesc, before class data. The
	// classDesc handle is baseWireHandle, the object handle baseWireHandle+1.
	b.byte(tcReference)
	b.u32(uint32(baseWireHandle + 1))
	or := readerForContent(t, b.bytes())
	if _, err := or.readContent(); err != nil {
		t.Fatalf("reference handle: unexpected error: %v", err)
	}
}

// TestCoverage_ClassNameStringReference covers readClassNameString's
// TC_REFERENCE branch: a field type name given as a back-reference to a prior
// string handle.
func TestCoverage_ClassNameStringReference(t *testing.T) {
	b := newStreamBuilder()
	b.byte(tcObject)
	b.byte(tcClassDesc)
	b.utf("com.example.Two")
	b.i64(0)
	b.byte(scSerializable)
	b.u16(2) // two object fields, second reuses first's type-name string
	b.byte('L')
	b.utf("a")
	b.byte(tcString) // first type-name string -> handle baseWireHandle+1
	b.utf("Ljava/lang/Object;")
	b.byte('L')
	b.utf("bb")
	b.byte(tcReference) // second type-name as reference to the string handle
	b.u32(uint32(baseWireHandle + 1))
	b.byte(tcEndBlockData)
	b.byte(tcNull)
	// Two object field values, both null.
	b.byte(tcNull)
	b.byte(tcNull)
	or := readerForContent(t, b.bytes())
	if _, err := or.readContent(); err != nil {
		t.Fatalf("class-name string reference: unexpected error: %v", err)
	}
}

func TestCoverage_ProxyClassDesc(t *testing.T) {
	var seen []string
	b := newStreamBuilder()
	b.byte(tcObject)
	b.byte(tcProxyClassDesc)
	b.u32(2) // two proxied interfaces
	b.utf("com.example.IFoo")
	b.utf("com.example.IBar")
	b.byte(tcEndBlockData) // proxy class annotation end
	b.byte(tcNull)         // super classDesc
	// Object body: no fields declared for a bare proxy desc chain -> class data
	// resolves to the proxy super (null), so nothing more to read.
	or := newObjectReader(bytes.NewReader(b.bytes()[4:]), func(name string) error {
		seen = append(seen, name)
		return nil
	})
	if _, err := or.readContent(); err != nil {
		t.Fatalf("proxy class desc: unexpected error: %v", err)
	}
	if len(seen) != 2 || seen[0] != "com.example.IFoo" || seen[1] != "com.example.IBar" {
		t.Fatalf("expected proxy interfaces filtered, got %v", seen)
	}
}

func TestCoverage_ExternalizableObject(t *testing.T) {
	b := newStreamBuilder()
	b.byte(tcObject)
	b.classDescShort("com.example.Ext", scSerializable|scExternalizable|scBlockData)
	// Externalizable + block data => a block-data annotation follows.
	b.byte(tcBlockData)
	b.byte(0x02)
	b.raw(0xAA, 0xBB)
	b.byte(tcEndBlockData)
	or := readerForContent(t, b.bytes())
	if _, err := or.readContent(); err != nil {
		t.Fatalf("externalizable: unexpected error: %v", err)
	}
}

func TestCoverage_TrivialGettersAndErrors(t *testing.T) {
	// SerialKiller.Profiling / Config getters.
	ResetConfigCache()
	sk, err := NewSerialKiller(bytes.NewReader(nil), "../testdata/serialkiller.conf")
	if err != nil {
		t.Fatalf("NewSerialKiller: %v", err)
	}
	if sk.Profiling() {
		t.Fatal("expected profiling false for serialkiller.conf")
	}
	if sk.Config() == nil {
		t.Fatal("expected non-nil Config")
	}

	// InvalidClassError.Error with and without ClassName.
	withName := (&InvalidClassError{ClassName: "com.X", Reason: "blocked"}).Error()
	if !strings.Contains(withName, "com.X; blocked") {
		t.Fatalf("unexpected error string: %q", withName)
	}
	noName := (&InvalidClassError{Reason: "blocked"}).Error()
	if noName != "blocked" {
		t.Fatalf("expected bare reason, got %q", noName)
	}

	// defaultLogger.Error/Info must not panic. Exercise the concrete
	// defaultLogger type directly (not just through the DefaultLogger
	// interface value) so both methods register as covered: Error writes to
	// the standard logger, Info is intentionally a no-op that discards.
	dl := defaultLogger{}
	dl.Info("info line is discarded")
	dl.Error("error line is emitted to the standard logger")
	DefaultLogger.Info("info line is discarded")
	DefaultLogger.Error("error line via interface")

	// IllegalStateError.Error/Unwrap.
	cause := errors.New("root cause")
	ise := &IllegalStateError{Message: "bad state", Cause: cause}
	if ise.Error() != "bad state" {
		t.Fatalf("IllegalStateError.Error: %q", ise.Error())
	}
	if !errors.Is(ise, cause) {
		t.Fatal("IllegalStateError should unwrap to its cause")
	}

	// NilRegExpsError.Error.
	nre := &NilRegExpsError{Name: "regExps"}
	if nre.Error() != "regExps" {
		t.Fatalf("NilRegExpsError.Error: %q", nre.Error())
	}

	// PatternSyntaxError.Error/Unwrap.
	perr := errors.New("compile boom")
	pse := &PatternSyntaxError{Pattern: "(", Err: perr}
	if !strings.Contains(pse.Error(), "(") {
		t.Fatalf("PatternSyntaxError.Error: %q", pse.Error())
	}
	if !errors.Is(pse, perr) {
		t.Fatal("PatternSyntaxError should unwrap to its cause")
	}
}
