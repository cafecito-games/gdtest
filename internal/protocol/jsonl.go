// Package protocol implements bounded, versioned JSON Lines transports.
package protocol

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Version is the daemon-to-worker protocol version.
const Version = 1

// DefaultMaxMessageSize bounds untrusted local messages to one mebibyte.
const DefaultMaxMessageSize = 1 << 20

// DefaultMaxNesting bounds recursive JSON containers before decoding.
const DefaultMaxNesting = 64

// Envelope is the common protocol framing.
type Envelope struct {
	Version   int             `json:"version"`
	Type      string          `json:"type"`
	Token     string          `json:"token,omitempty"`
	RequestID string          `json:"request_id,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// Encoder writes one JSON value per line.
type Encoder struct{ encoder *json.Encoder }

// NewEncoder creates a JSONL encoder.
func NewEncoder(writer io.Writer) *Encoder {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return &Encoder{encoder: encoder}
}

// Encode writes one complete message.
func (e *Encoder) Encode(value any) error { return e.encoder.Encode(value) }

// Decoder reads size-bounded JSONL messages.
type Decoder struct {
	reader  *bufio.Reader
	maxSize int
}

// NewDecoder creates a bounded decoder. Non-positive limits use the default.
func NewDecoder(reader io.Reader, maxSize int) *Decoder {
	if maxSize <= 0 {
		maxSize = DefaultMaxMessageSize
	}
	return &Decoder{reader: bufio.NewReaderSize(reader, min(maxSize+1, 64*1024)), maxSize: maxSize}
}

// Decode reads exactly one JSON object and rejects oversized or trailing data.
func (d *Decoder) Decode(value any) error {
	line, err := d.reader.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		return fmt.Errorf("protocol message exceeds %d bytes", d.maxSize)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if len(line) == 0 && errors.Is(err, io.EOF) {
		return io.EOF
	}
	if len(line) > d.maxSize {
		return fmt.Errorf("protocol message exceeds %d bytes", d.maxSize)
	}
	if depth := jsonNesting(line); depth > DefaultMaxNesting {
		return fmt.Errorf("protocol message nesting %d exceeds %d", depth, DefaultMaxNesting)
	}
	if decodeErr := json.Unmarshal(line, value); decodeErr != nil {
		return fmt.Errorf("decode protocol message: %w", decodeErr)
	}
	return nil
}

func jsonNesting(data []byte) int {
	depth := 0
	maximum := 0
	inString := false
	escaped := false
	for _, current := range data {
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if current == '\\' {
				escaped = true
				continue
			}
			if current == '"' {
				inString = false
			}
			continue
		}
		switch current {
		case '"':
			inString = true
		case '{', '[':
			depth++
			maximum = max(maximum, depth)
		case '}', ']':
			depth--
		}
	}
	return maximum
}
