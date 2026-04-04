package protocol

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// Encoder writes NDJSON messages to a writer. Safe for concurrent use.
type Encoder struct {
	mu sync.Mutex
	w  io.Writer
}

func NewEncoder(w io.Writer) *Encoder {
	return &Encoder{w: w}
}

func (e *Encoder) Encode(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	data = append(data, '\n')
	_, err = e.w.Write(data)
	return err
}

// Decoder reads NDJSON messages from a reader.
type Decoder struct {
	scanner *bufio.Scanner
}

func NewDecoder(r io.Reader) *Decoder {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return &Decoder{scanner: scanner}
}

func (d *Decoder) Decode(v any) error {
	if !d.scanner.Scan() {
		if err := d.scanner.Err(); err != nil {
			return err
		}
		return io.EOF
	}
	data := make([]byte, len(d.scanner.Bytes()))
	copy(data, d.scanner.Bytes())
	return json.Unmarshal(data, v)
}

func (d *Decoder) DecodeRaw() (json.RawMessage, error) {
	if !d.scanner.Scan() {
		if err := d.scanner.Err(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	raw := make(json.RawMessage, len(d.scanner.Bytes()))
	copy(raw, d.scanner.Bytes())
	return raw, nil
}

// TypeOf extracts the "type" field from a raw JSON message.
func TypeOf(raw json.RawMessage) (string, error) {
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return "", fmt.Errorf("extract type: %w", err)
	}
	if envelope.Type == "" {
		return "", fmt.Errorf("message has no type field")
	}
	return envelope.Type, nil
}
