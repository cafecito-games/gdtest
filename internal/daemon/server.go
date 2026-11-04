package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/cafecito-games/gdtest/internal/protocol"
)

// Server hosts one compatible daemon namespace.
type Server struct {
	Namespace Namespace
	Handler   Handler
	Logger    *Logger
	MaxSize   int

	mu        sync.Mutex
	cancel    context.CancelFunc
	listener  net.Listener
	startedAt time.Time
	conns     map[net.Conn]struct{}
	wait      sync.WaitGroup
}

// Serve acquires the namespace and serves until ctx cancellation or daemon.stop.
func (s *Server) Serve(ctx context.Context) error {
	if err := s.Namespace.Prepare(); err != nil {
		return err
	}
	lock, err := AcquireLock(s.Namespace.LockPath)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	if err := os.Remove(s.Namespace.SocketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale daemon socket: %w", err)
	}
	listener, err := net.Listen("unix", s.Namespace.SocketPath)
	if err != nil {
		return fmt.Errorf("listen on daemon socket: %w", err)
	}
	if err := os.Chmod(s.Namespace.SocketPath, 0o600); err != nil {
		_ = listener.Close()
		return fmt.Errorf("secure daemon socket: %w", err)
	}
	serveCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.cancel = cancel
	s.listener = listener
	s.startedAt = time.Now().UTC()
	s.conns = make(map[net.Conn]struct{})
	s.mu.Unlock()
	defer s.cleanup()
	metadata := Metadata{
		Protocol: s.Namespace.Protocol, BuildID: s.Namespace.BuildID, PID: os.Getpid(),
		SocketPath: s.Namespace.SocketPath, StartedAt: s.startedAt,
	}
	if err := s.Namespace.writeMetadata(metadata); err != nil {
		return err
	}
	s.log(map[string]any{"event": "daemon_started", "pid": metadata.PID})
	go func() {
		<-serveCtx.Done()
		s.closeTransport()
	}()
	for {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			if serveCtx.Err() != nil || errors.Is(acceptErr, net.ErrClosed) {
				break
			}
			return fmt.Errorf("accept daemon client: %w", acceptErr)
		}
		s.mu.Lock()
		s.conns[connection] = struct{}{}
		s.wait.Add(1)
		s.mu.Unlock()
		go s.handleConnection(serveCtx, connection)
	}
	s.wait.Wait()
	return nil
}

// Stop requests a clean server shutdown.
func (s *Server) Stop() {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Server) handleConnection(ctx context.Context, connection net.Conn) {
	defer s.wait.Done()
	defer func() {
		s.mu.Lock()
		delete(s.conns, connection)
		s.mu.Unlock()
		_ = connection.Close()
	}()
	decoder := protocol.NewDecoder(connection, s.MaxSize)
	encoder := protocol.NewEncoder(connection)
	for {
		var message Message
		if err := decoder.Decode(&message); err != nil {
			if errors.Is(err, io.EOF) || ctx.Err() != nil {
				return
			}
			_ = encodeMessage(encoder, MessageError, "", "", true, map[string]any{"message": err.Error()})
			s.log(map[string]any{"event": "client_protocol_error", "error": err.Error()})
			if strings.Contains(err.Error(), "exceeds") {
				return
			}
			continue
		}
		if err := validateRequest(message); err != nil {
			_ = encodeMessage(encoder, MessageError, message.RequestID, message.Method, true, map[string]any{"message": err.Error()})
			continue
		}
		request := Request{ID: message.RequestID, Method: message.Method, Payload: message.Payload}
		if request.Method == "daemon.status" {
			result := map[string]any{
				"pid": os.Getpid(), "protocol": ProtocolVersion, "build_id": s.Namespace.BuildID,
				"started_at": s.startedAt, "uptime_ns": time.Since(s.startedAt).Nanoseconds(),
			}
			if err := encodeMessage(encoder, MessageResponse, request.ID, request.Method, true, result); err != nil {
				return
			}
			continue
		}
		if request.Method == "daemon.stop" {
			if err := encodeMessage(encoder, MessageResponse, request.ID, request.Method, true, map[string]any{"stopping": true}); err != nil {
				return
			}
			s.Stop()
			return
		}
		if s.Handler == nil {
			_ = encodeMessage(encoder, MessageError, request.ID, request.Method, true, map[string]any{"message": "unsupported daemon method"})
			continue
		}
		emit := func(event any) error {
			return encodeMessage(encoder, MessageEvent, request.ID, request.Method, false, event)
		}
		result, err := s.callHandler(ctx, request, emit)
		if err != nil {
			if encodeErr := encodeMessage(encoder, MessageError, request.ID, request.Method, true, map[string]any{"message": err.Error()}); encodeErr != nil {
				return
			}
			continue
		}
		if err := encodeMessage(encoder, MessageResponse, request.ID, request.Method, true, result); err != nil {
			return
		}
	}
}

func (s *Server) callHandler(ctx context.Context, request Request, emit EmitFunc) (result any, err error) {
	if s.Handler == nil {
		return nil, fmt.Errorf("unsupported daemon method %q", request.Method)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			s.log(map[string]any{
				"event": "handler_panic", "request_id": request.ID,
				"method": request.Method, "panic": fmt.Sprint(recovered), "stack": string(debug.Stack()),
			})
			result = nil
			err = fmt.Errorf("daemon handler panic while serving %s", request.Method)
		}
	}()
	return s.Handler.Handle(ctx, request, emit)
}

func validateRequest(message Message) error {
	if message.Version != ProtocolVersion {
		return fmt.Errorf("client protocol version %d does not match %d", message.Version, ProtocolVersion)
	}
	if message.Type != MessageRequest {
		return fmt.Errorf("expected request message, got %q", message.Type)
	}
	if message.RequestID == "" {
		return errors.New("request_id is required")
	}
	if message.Method == "" {
		return errors.New("method is required")
	}
	return nil
}

func encodeMessage(encoder *protocol.Encoder, messageType, requestID, method string, terminal bool, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode daemon payload: %w", err)
	}
	return encoder.Encode(Message{
		Version: ProtocolVersion, Type: messageType, RequestID: requestID,
		Method: method, Payload: data, Terminal: terminal,
	})
}

func (s *Server) closeTransport() {
	s.mu.Lock()
	listener := s.listener
	connections := make([]net.Conn, 0, len(s.conns))
	for connection := range s.conns {
		connections = append(connections, connection)
	}
	s.mu.Unlock()
	if listener != nil {
		_ = listener.Close()
	}
	for _, connection := range connections {
		_ = connection.Close()
	}
}

func (s *Server) cleanup() {
	s.Stop()
	s.closeTransport()
	s.wait.Wait()
	_ = os.Remove(s.Namespace.MetadataPath)
	_ = os.Remove(s.Namespace.SocketPath)
	s.log(map[string]any{"event": "daemon_stopped"})
}

func (s *Server) log(fields map[string]any) {
	if s.Logger != nil {
		_ = s.Logger.Write(fields)
	}
}
