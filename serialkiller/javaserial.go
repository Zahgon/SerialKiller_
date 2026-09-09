package serialkiller

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Java Object Serialization Stream Protocol constants (see
// java.io.ObjectStreamConstants). Only the subset needed to walk the streams
// SerialKiller filters is implemented.
const (
	streamMagic   uint16 = 0xACED
	streamVersion uint16 = 5

	tcNull          = 0x70
	tcReference     = 0x71
	tcClassDesc     = 0x72
	tcObject        = 0x73
	tcString        = 0x74
	tcArray         = 0x75
	tcClass         = 0x76
	tcBlockData     = 0x77
	tcEndBlockData  = 0x78
	tcReset         = 0x79
	tcBlockDataLong = 0x7A
	tcException     = 0x7B
	tcLongString    = 0x7C
	tcProxyClassDesc = 0x7D
	tcEnum          = 0x7E

	baseWireHandle = 0x7E0000

	scWriteMethod  = 0x01
	scSerializable = 0x02
	scExternalizable = 0x04
	scBlockData    = 0x08
	scEnum         = 0x10
)

// classFilter is invoked once for every class descriptor name encountered while
// walking the stream, in the exact order java.io.ObjectInputStream would invoke
// resolveClass. Returning a non-nil error aborts the walk immediately,
// mirroring resolveClass throwing (which propagates out of readObject).
type classFilter func(name string) error

// objectReader walks a Java serialization stream. It reproduces the traversal
// order of ObjectInputStream closely enough that the class filter observes the
// same class names in the same order, and it decodes the small set of values
// SerialKiller's tests round-trip (top-level String and Integer). It maintains
// a handle table so back-references (TC_REFERENCE) resolve, matching Java.
type objectReader struct {
	r      io.Reader
	filter classFilter

	handles []interface{}
}

func newObjectReader(r io.Reader, filter classFilter) *objectReader {
	return &objectReader{r: r, filter: filter}
}

// classDesc is the decoded shape of a class descriptor, retaining the field
// layout needed to walk instance data.
type classDesc struct {
	name   string
	flags  byte
	fields []fieldDesc
	super  *classDesc
	isNull bool
}

type fieldDesc struct {
	typecode byte
	name     string
}

func (or *objectReader) newHandle(v interface{}) int {
	or.handles = append(or.handles, v)
	return baseWireHandle + len(or.handles) - 1
}

func (or *objectReader) setHandle(h int, v interface{}) {
	idx := h - baseWireHandle
	if idx >= 0 && idx < len(or.handles) {
		or.handles[idx] = v
	}
}

func (or *objectReader) getHandle(h int) interface{} {
	idx := h - baseWireHandle
	if idx >= 0 && idx < len(or.handles) {
		return or.handles[idx]
	}
	return nil
}

func (or *objectReader) readFull(n int) ([]byte, error) {
	buf := make([]byte, n)
	if _, err := io.ReadFull(or.r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func (or *objectReader) readByte() (byte, error) {
	b, err := or.readFull(1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

func (or *objectReader) readU16() (uint16, error) {
	b, err := or.readFull(2)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(b), nil
}

func (or *objectReader) readU32() (uint32, error) {
	b, err := or.readFull(4)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(b), nil
}

func (or *objectReader) readI32() (int32, error) {
	v, err := or.readU32()
	return int32(v), err
}

func (or *objectReader) readI64() (int64, error) {
	b, err := or.readFull(8)
	if err != nil {
		return 0, err
	}
	return int64(binary.BigEndian.Uint64(b)), nil
}

// readUTF reads a 2-byte-length-prefixed modified-UTF-8 string. For the ASCII
// content in these streams, plain byte-to-string conversion is exact.
func (or *objectReader) readUTF() (string, error) {
	n, err := or.readU16()
	if err != nil {
		return "", err
	}
	b, err := or.readFull(int(n))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// readLongUTF reads an 8-byte-length-prefixed string (TC_LONGSTRING).
func (or *objectReader) readLongUTF() (string, error) {
	n, err := or.readI64()
	if err != nil {
		return "", err
	}
	b, err := or.readFull(int(n))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ReadStream validates the stream header and reads exactly one top-level
// content element, mirroring a single ObjectInputStream.readObject() call. It
// returns the decoded value for the elements the tests round-trip (String,
// Integer) and drives the class filter over every class descriptor.
func (or *objectReader) ReadStream() (interface{}, error) {
	magic, err := or.readU16()
	if err != nil {
		return nil, err
	}
	version, err := or.readU16()
	if err != nil {
		return nil, err
	}
	if magic != streamMagic || version != streamVersion {
		return nil, fmt.Errorf("invalid stream header: magic=%#x version=%#x", magic, version)
	}
	return or.readContent()
}

// readNext reads a single content element when the stream header has already
// been consumed (used for successive readObject() calls on the same stream).
func (or *objectReader) readContent() (interface{}, error) {
	tc, err := or.readByte()
	if err != nil {
		return nil, err
	}
	return or.readContentWithTag(tc)
}

func (or *objectReader) readContentWithTag(tc byte) (interface{}, error) {
	switch tc {
	case tcNull:
		return nil, nil
	case tcReference:
		h, err := or.readU32()
		if err != nil {
			return nil, err
		}
		return or.getHandle(int(h)), nil
	case tcString:
		s, err := or.readUTF()
		if err != nil {
			return nil, err
		}
		or.newHandle(s)
		return s, nil
	case tcLongString:
		s, err := or.readLongUTF()
		if err != nil {
			return nil, err
		}
		or.newHandle(s)
		return s, nil
	case tcObject:
		return or.readOrdinaryObject()
	case tcArray:
		return or.readArray()
	case tcEnum:
		return or.readEnum()
	case tcClass:
		_, err := or.readClassDesc()
		return nil, err
	case tcClassDesc, tcProxyClassDesc:
		return or.readClassDescWithTag(tc)
	case tcBlockData:
		return or.readBlockData()
	case tcBlockDataLong:
		return or.readBlockDataLong()
	case tcReset:
		or.handles = or.handles[:0]
		return or.readContent()
	case tcException:
		return nil, fmt.Errorf("TC_EXCEPTION in stream")
	default:
		return nil, fmt.Errorf("unexpected type code %#x", tc)
	}
}

func (or *objectReader) readClassDesc() (*classDesc, error) {
	tc, err := or.readByte()
	if err != nil {
		return nil, err
	}
	return or.readClassDescWithTag(tc)
}

func (or *objectReader) readClassDescWithTag(tc byte) (*classDesc, error) {
	switch tc {
	case tcNull:
		return &classDesc{isNull: true}, nil
	case tcReference:
		h, err := or.readU32()
		if err != nil {
			return nil, err
		}
		v := or.getHandle(int(h))
		if cd, ok := v.(*classDesc); ok {
			return cd, nil
		}
		return &classDesc{isNull: true}, nil
	case tcProxyClassDesc:
		return or.readProxyClassDesc()
	case tcClassDesc:
		return or.readNonProxyClassDesc()
	default:
		return nil, fmt.Errorf("expected class descriptor, got %#x", tc)
	}
}

func (or *objectReader) readNonProxyClassDesc() (*classDesc, error) {
	name, err := or.readUTF()
	if err != nil {
		return nil, err
	}
	// serialVersionUID (8 bytes)
	if _, err := or.readI64(); err != nil {
		return nil, err
	}
	cd := &classDesc{name: name}
	or.newHandle(cd)

	// Invoke the filter for this class, mirroring resolveClass being called
	// for each ObjectStreamClass read from the stream.
	if or.filter != nil {
		if err := or.filter(name); err != nil {
			return nil, err
		}
	}

	flags, err := or.readByte()
	if err != nil {
		return nil, err
	}
	cd.flags = flags

	fieldCount, err := or.readU16()
	if err != nil {
		return nil, err
	}
	for i := 0; i < int(fieldCount); i++ {
		tcode, err := or.readByte()
		if err != nil {
			return nil, err
		}
		fname, err := or.readUTF()
		if err != nil {
			return nil, err
		}
		fd := fieldDesc{typecode: tcode, name: fname}
		if tcode == '[' || tcode == 'L' {
			// Field's class name is written as a (new or referenced) string.
			if _, err := or.readClassNameString(); err != nil {
				return nil, err
			}
		}
		cd.fields = append(cd.fields, fd)
	}

	// classAnnotation: sequence of content ending with TC_ENDBLOCKDATA.
	if err := or.readClassAnnotation(); err != nil {
		return nil, err
	}

	// superClassDesc
	super, err := or.readClassDesc()
	if err != nil {
		return nil, err
	}
	cd.super = super
	return cd, nil
}

func (or *objectReader) readProxyClassDesc() (*classDesc, error) {
	cd := &classDesc{name: ""}
	or.newHandle(cd)

	count, err := or.readU32()
	if err != nil {
		return nil, err
	}
	for i := 0; i < int(count); i++ {
		iface, err := or.readUTF()
		if err != nil {
			return nil, err
		}
		if or.filter != nil {
			if err := or.filter(iface); err != nil {
				return nil, err
			}
		}
	}
	if err := or.readClassAnnotation(); err != nil {
		return nil, err
	}
	super, err := or.readClassDesc()
	if err != nil {
		return nil, err
	}
	cd.super = super
	return cd, nil
}

// readClassNameString reads a field type name reference, which is either a new
// TC_STRING/TC_LONGSTRING or a TC_REFERENCE to an already-read string.
func (or *objectReader) readClassNameString() (string, error) {
	tc, err := or.readByte()
	if err != nil {
		return "", err
	}
	switch tc {
	case tcString:
		s, err := or.readUTF()
		if err != nil {
			return "", err
		}
		or.newHandle(s)
		return s, nil
	case tcLongString:
		s, err := or.readLongUTF()
		if err != nil {
			return "", err
		}
		or.newHandle(s)
		return s, nil
	case tcReference:
		h, err := or.readU32()
		if err != nil {
			return "", err
		}
		if s, ok := or.getHandle(int(h)).(string); ok {
			return s, nil
		}
		return "", nil
	default:
		return "", fmt.Errorf("expected class-name string, got %#x", tc)
	}
}

// readClassAnnotation consumes content objects until TC_ENDBLOCKDATA.
func (or *objectReader) readClassAnnotation() error {
	for {
		tc, err := or.readByte()
		if err != nil {
			return err
		}
		if tc == tcEndBlockData {
			return nil
		}
		if _, err := or.readContentWithTag(tc); err != nil {
			return err
		}
	}
}

func (or *objectReader) readOrdinaryObject() (interface{}, error) {
	cd, err := or.readClassDesc()
	if err != nil {
		return nil, err
	}
	handle := or.newHandle(nil)

	value, err := or.readClassData(cd)
	if err != nil {
		return nil, err
	}
	or.setHandle(handle, value)
	return value, nil
}

// readClassData reads the serialized field values for the class hierarchy from
// the topmost superclass down, mirroring ObjectInputStream defaultReadObject
// ordering. It returns a decoded value for the small classes the tests
// round-trip (java.lang.Integer -> int32); other classes decode structurally
// and return nil.
func (or *objectReader) readClassData(cd *classDesc) (interface{}, error) {
	// Build the hierarchy chain from super-most to the class itself.
	var chain []*classDesc
	for c := cd; c != nil && !c.isNull; c = c.super {
		chain = append([]*classDesc{c}, chain...)
	}

	var result interface{}
	for _, c := range chain {
		if c.flags&scExternalizable != 0 {
			// Externalizable: block-data or object content until end block.
			if c.flags&scBlockData != 0 {
				if err := or.readClassAnnotation(); err != nil {
					return nil, err
				}
			}
			continue
		}

		vals, err := or.readFields(c)
		if err != nil {
			return nil, err
		}
		if c.name == "java.lang.Integer" {
			if v, ok := vals["value"]; ok {
				result = v
			}
		}

		// If the class defines a writeObject method, custom data follows,
		// terminated by TC_ENDBLOCKDATA.
		if c.flags&scWriteMethod != 0 {
			if err := or.readClassAnnotation(); err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

// readFields reads the primitive and object field values declared by a class
// descriptor. Primitive fields come first in field order, then object fields.
// Java sorts fields (primitives first) when writing; the field list in the
// descriptor is already in that written order.
func (or *objectReader) readFields(cd *classDesc) (map[string]interface{}, error) {
	out := make(map[string]interface{})
	for _, f := range cd.fields {
		switch f.typecode {
		case 'B':
			b, err := or.readByte()
			if err != nil {
				return nil, err
			}
			out[f.name] = int8(b)
		case 'C':
			v, err := or.readU16()
			if err != nil {
				return nil, err
			}
			out[f.name] = rune(v)
		case 'D':
			v, err := or.readI64()
			if err != nil {
				return nil, err
			}
			out[f.name] = v
		case 'F':
			v, err := or.readU32()
			if err != nil {
				return nil, err
			}
			out[f.name] = v
		case 'I':
			v, err := or.readI32()
			if err != nil {
				return nil, err
			}
			out[f.name] = v
		case 'J':
			v, err := or.readI64()
			if err != nil {
				return nil, err
			}
			out[f.name] = v
		case 'S':
			v, err := or.readU16()
			if err != nil {
				return nil, err
			}
			out[f.name] = int16(v)
		case 'Z':
			b, err := or.readByte()
			if err != nil {
				return nil, err
			}
			out[f.name] = b != 0
		case '[', 'L':
			v, err := or.readContent()
			if err != nil {
				return nil, err
			}
			out[f.name] = v
		default:
			return nil, fmt.Errorf("unknown field typecode %q", f.typecode)
		}
	}
	return out, nil
}

func (or *objectReader) readArray() (interface{}, error) {
	cd, err := or.readClassDesc()
	if err != nil {
		return nil, err
	}
	or.newHandle(nil)

	size, err := or.readI32()
	if err != nil {
		return nil, err
	}

	// Element typecode is the second character of the array class name, e.g.
	// "[I" -> 'I', "[Ljava.lang.Object;" -> 'L'.
	elemType := byte('L')
	if len(cd.name) >= 2 {
		elemType = cd.name[1]
	}

	for i := 0; i < int(size); i++ {
		switch elemType {
		case 'B', 'Z':
			if _, err := or.readByte(); err != nil {
				return nil, err
			}
		case 'C', 'S':
			if _, err := or.readU16(); err != nil {
				return nil, err
			}
		case 'I', 'F':
			if _, err := or.readU32(); err != nil {
				return nil, err
			}
		case 'J', 'D':
			if _, err := or.readI64(); err != nil {
				return nil, err
			}
		case '[', 'L':
			if _, err := or.readContent(); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unknown array element type %q", elemType)
		}
	}
	return nil, nil
}

func (or *objectReader) readEnum() (interface{}, error) {
	if _, err := or.readClassDesc(); err != nil {
		return nil, err
	}
	or.newHandle(nil)
	// enum constant name (a string)
	name, err := or.readContent()
	if err != nil {
		return nil, err
	}
	return name, nil
}

func (or *objectReader) readBlockData() (interface{}, error) {
	n, err := or.readByte()
	if err != nil {
		return nil, err
	}
	if _, err := or.readFull(int(n)); err != nil {
		return nil, err
	}
	return nil, nil
}

func (or *objectReader) readBlockDataLong() (interface{}, error) {
	n, err := or.readU32()
	if err != nil {
		return nil, err
	}
	if _, err := or.readFull(int(n)); err != nil {
		return nil, err
	}
	return nil, nil
}
