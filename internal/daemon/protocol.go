package daemon

import (
	"context"
	"encoding/json"
)

// MessageType values define the client protocol stream.
const (
	MessageRequest  = "request"
	MessageEvent    = "event"
	MessageResponse = "response"
	MessageError    = "error"
)

// Message is one versioned JSONL client protocol record.
type Message struct {
	Version   int             `json:"version"`
	Type      string          `json:"type"`
	RequestID string          `json:"request_id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Terminal  bool            `json:"terminal,omitempty"`
}

// Request is one validated daemon operation.
type Request struct {
	ID      string
	Method  string
	Payload json.RawMessage
}

// EmitFunc streams one immutable event for a request.
type EmitFunc func(any) error

// Handler owns project/session operations outside the daemon transport.
type Handler interface {
	Handle(context.Context, Request, EmitFunc) (any, error)
}

// HandlerFunc adapts a function to Handler.
type HandlerFunc func(context.Context, Request, EmitFunc) (any, error)

// Handle implements Handler.
func (f HandlerFunc) Handle(ctx context.Context, request Request, emit EmitFunc) (any, error) {
	return f(ctx, request, emit)
}
