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

// DefaultMaxMessageSize bounds untrusted local messages while accommodating
// large real-world project manifests (for example, hundreds of test files).
const DefaultMaxMessageSize = 16 << 20

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
	line, err := d.readBoundedLine()
	if err != nil {
		return err
	}
	if depth := jsonNesting(line); depth > DefaultMaxNesting {
		return fmt.Errorf("protocol message nesting %d exceeds %d", depth, DefaultMaxNesting)
	}
	if decodeErr := json.Unmarshal(line, value); decodeErr != nil {
		return fmt.Errorf("decode protocol message: %w", decodeErr)
	}
	return nil
}

func (d *Decoder) readBoundedLine() ([]byte, error) {
	line := make([]byte, 0, min(d.maxSize, 64*1024))
	for {
		fragment, err := d.reader.ReadSlice('\n')
		if len(line)+len(fragment) > d.maxSize {
			return nil, fmt.Errorf("protocol message exceeds %d bytes", d.maxSize)
		}
		line = append(line, fragment...)
		switch {
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && len(line) == 0:
			return nil, io.EOF
		case errors.Is(err, io.EOF):
			return line, nil
		case err != nil:
			return nil, err
		default:
			return line, nil
		}
	}
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
