package archive

import "bytes"

// Inspection needs a small number of flags, not the complete archive listing.
// Bound both malformed long records and the diagnostic tail from the tool.
type inspectionOutput struct {
	line     []byte
	tail     []byte
	required bool
}

func (o *inspectionOutput) Write(data []byte) (int, error) {
	const diagnosticLimit = 8192
	n := len(data)
	if len(data) >= diagnosticLimit {
		o.tail = append(o.tail[:0], data[len(data)-diagnosticLimit:]...)
	} else {
		if excess := len(o.tail) + len(data) - diagnosticLimit; excess > 0 {
			copy(o.tail, o.tail[excess:])
			o.tail = o.tail[:len(o.tail)-excess]
		}
		o.tail = append(o.tail, data...)
	}
	for _, value := range data {
		if value == '\n' || value == '\r' {
			o.flush()
			continue
		}
		if len(o.line) < 64<<10 {
			o.line = append(o.line, value)
		}
	}
	return n, nil
}

func (o *inspectionOutput) flush() {
	for i, value := range o.line {
		if value >= 'A' && value <= 'Z' {
			o.line[i] = value + ('a' - 'A')
		}
	}
	if bytes.Contains(o.line, []byte("wrong password")) || bytes.Contains(o.line, []byte("incorrect password")) || bytes.Contains(o.line, []byte("data error in encrypted file")) || bytes.Contains(o.line, []byte("encrypted archive")) {
		o.required = true
	}
	name, value, ok := bytes.Cut(o.line, []byte("="))
	if ok && bytes.Equal(bytes.TrimSpace(name), []byte("encrypted")) && bytes.Equal(bytes.TrimSpace(value), []byte("+")) {
		o.required = true
	}
	o.line = o.line[:0]
}
