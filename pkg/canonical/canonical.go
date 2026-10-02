// Package canonical implements RFC 8785 (JSON Canonicalization Scheme) for the
// subset of JSON Pumat allows in signed and hashed objects (spec §7.4).
//
// Floating-point numbers are rejected: signed payloads carry integers in base
// units or decimal strings, which keeps canonicalization trivially portable.
package canonical

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"
)

const maxSafeInteger = 1<<53 - 1

var ErrNotCanonical = errors.New("canonical: payload is not in canonical form")

// Marshal encodes v as canonical JSON. v is first encoded with encoding/json,
// so struct tags apply.
func Marshal(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return Canonicalize(raw)
}

// Canonicalize re-encodes arbitrary JSON bytes into canonical form.
func Canonicalize(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("canonical: %w", err)
	}
	if dec.More() {
		return nil, errors.New("canonical: trailing data")
	}
	var buf bytes.Buffer
	if err := encode(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Check returns nil if raw is already canonical. Non-canonical input, including
// duplicate keys, extra whitespace and floats, is rejected.
func Check(raw []byte) error {
	c, err := Canonicalize(raw)
	if err != nil {
		return err
	}
	if !bytes.Equal(c, raw) {
		return ErrNotCanonical
	}
	return nil
}

// Unmarshal checks that raw is canonical and decodes it into v, rejecting
// unknown fields.
func Unmarshal(raw []byte, v any) error {
	if err := Check(raw); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func encode(buf *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if t {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case json.Number:
		return encodeNumber(buf, t)
	case string:
		return encodeString(buf, t)
	case []any:
		buf.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := encode(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return lessUTF16(keys[i], keys[j]) })
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := encodeString(buf, k); err != nil {
				return err
			}
			buf.WriteByte(':')
			if err := encode(buf, t[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		return fmt.Errorf("canonical: unsupported type %T", v)
	}
	return nil
}

func encodeNumber(buf *bytes.Buffer, n json.Number) error {
	i, err := strconv.ParseInt(n.String(), 10, 64)
	if err != nil {
		return fmt.Errorf("canonical: only integers are allowed, got %q", n.String())
	}
	if i > maxSafeInteger || i < -maxSafeInteger {
		return fmt.Errorf("canonical: integer %d outside safe range", i)
	}
	buf.WriteString(strconv.FormatInt(i, 10))
	return nil
}

func encodeString(buf *bytes.Buffer, s string) error {
	if !utf8.ValidString(s) {
		return errors.New("canonical: invalid UTF-8 string")
	}
	buf.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			buf.WriteString(`\"`)
		case '\\':
			buf.WriteString(`\\`)
		case '\b':
			buf.WriteString(`\b`)
		case '\f':
			buf.WriteString(`\f`)
		case '\n':
			buf.WriteString(`\n`)
		case '\r':
			buf.WriteString(`\r`)
		case '\t':
			buf.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(buf, `\u%04x`, r)
			} else {
				buf.WriteRune(r)
			}
		}
	}
	buf.WriteByte('"')
	return nil
}

// lessUTF16 orders strings by UTF-16 code units, as RFC 8785 requires.
func lessUTF16(a, b string) bool {
	ua := utf16.Encode([]rune(a))
	ub := utf16.Encode([]rune(b))
	n := min(len(ua), len(ub))
	for i := 0; i < n; i++ {
		if ua[i] != ub[i] {
			return ua[i] < ub[i]
		}
	}
	return len(ua) < len(ub)
}
