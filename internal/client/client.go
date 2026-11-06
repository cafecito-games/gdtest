// Package client connects commands to the compatible user-scoped daemon.
package client

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"time"

	"github.com/cafecito-games/gdtest/internal/daemon"
	"github.com/cafecito-games/gdtest/internal/protocol"
)

// Starter launches a daemon for one exact namespace.
type Starter func(context.Context, daemon.Namespace) error

// Client performs one request per authenticated local connection.
type Client struct {
	Namespace    daemon.Namespace
	Starter      Starter
	StartTimeout time.Duration
	MaxSize      int
	dialContext  func(context.Context, string, string) (net.Conn, error)
}

// Call streams events and returns the exactly one terminal response payload.
func (c Client) Call(ctx context.Context, method string, payload any, onEvent func(json.RawMessage) error) (json.RawMessage, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode daemon request: %w", err)
	}
	requestID, err := requestID()
	if err != nil {
		return nil, err
	}
	request := daemon.Message{
		Version: daemon.ProtocolVersion, Type: daemon.MessageRequest,
		RequestID: requestID, Method: method, Payload: data,
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode daemon request envelope: %w", err)
	}
	maxSize := c.MaxSize
	if maxSize <= 0 {
		maxSize = protocol.DefaultMaxMessageSize
	}
	if len(encoded)+1 > maxSize {
		return nil, fmt.Errorf("daemon request for %s is %d bytes and exceeds the %d-byte protocol limit", method, len(encoded)+1, maxSize)
	}
	if err := c.Ensure(ctx); err != nil {
		return nil, err
	}
	var connection net.Conn
	for attempt := range 2 {
		connection, err = c.dial(ctx)
		if err == nil {
			break
		}
		if attempt == 1 {
			return nil, fmt.Errorf("connect to daemon after readiness check: %w", err)
		}
		if ensureErr := c.Ensure(ctx); ensureErr != nil {
			return nil, fmt.Errorf("reconnect to daemon: %w", ensureErr)
		}
	}
	defer func() { _ = connection.Close() }()
	stopCancellation := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stopCancellation()
	encoder := protocol.NewEncoder(connection)
	decoder := protocol.NewDecoder(connection, c.MaxSize)
	if err := encoder.Encode(request); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("send daemon request: %w", err)
	}
	for {
		var message daemon.Message
		if err := decoder.Decode(&message); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("read daemon response: %w", err)
		}
		if message.Version != daemon.ProtocolVersion {
			return nil, fmt.Errorf("daemon protocol version %d does not match %d", message.Version, daemon.ProtocolVersion)
		}
		if message.RequestID != requestID {
			return nil, fmt.Errorf("daemon response request ID %q does not match %q", message.RequestID, requestID)
		}
		switch message.Type {
		case daemon.MessageEvent:
			if message.Terminal {
				return nil, errors.New("daemon event cannot be terminal")
			}
			if onEvent != nil {
				if err := onEvent(message.Payload); err != nil {
					return nil, err
				}
			}
		case daemon.MessageResponse:
			if !message.Terminal {
				return nil, errors.New("daemon response must be terminal")
			}
			return message.Payload, nil
		case daemon.MessageError:
			if !message.Terminal {
				return nil, errors.New("daemon error must be terminal")
			}
			var remote struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal(message.Payload, &remote); err != nil || remote.Message == "" {
				return nil, fmt.Errorf("daemon error: %s", message.Payload)
			}
			return nil, errors.New(remote.Message)
		default:
			return nil, fmt.Errorf("unexpected daemon message type %q", message.Type)
		}
	}
}

// Ensure connects to an existing daemon or starts exactly this build and waits for readiness.
func (c Client) Ensure(ctx context.Context) error {
	if connection, err := c.dial(ctx); err == nil {
		_ = connection.Close()
		return nil
	}
	if c.Starter == nil {
		return errors.New("compatible daemon is not running and no starter is configured")
	}
	if err := c.Starter(ctx, c.Namespace); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	timeout := c.StartTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		connection, err := c.dial(ctx)
		if err == nil {
			_ = connection.Close()
			metadata, metadataErr := c.Namespace.ReadMetadata()
			if metadataErr != nil {
				lastErr = metadataErr
			} else if metadata.Protocol == daemon.ProtocolVersion && metadata.BuildID == c.Namespace.BuildID {
				return nil
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("daemon did not become ready: %w", lastErr)
		case <-ticker.C:
		}
	}
}

func (c Client) dial(ctx context.Context) (net.Conn, error) {
	if c.dialContext != nil {
		return c.dialContext(ctx, "unix", c.Namespace.SocketPath)
	}
	return (&net.Dialer{}).DialContext(ctx, "unix", c.Namespace.SocketPath)
}

// ExecutableStarter starts the current binary's internal daemon command.
func ExecutableStarter(executable string) Starter {
	return func(_ context.Context, namespace daemon.Namespace) error {
		if err := namespace.Prepare(); err != nil {
			return err
		}
		path := executable
		if path == "" {
			var err error
			path, err = os.Executable()
			if err != nil {
				return fmt.Errorf("locate gdtest executable: %w", err)
			}
		}
		log, err := os.OpenFile(namespace.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("open daemon startup log: %w", err)
		}
		command := exec.Command(path, "daemon", "serve", "--namespace", namespace.Root)
		detachCommand(command)
		command.Stdin = nil
		command.Stdout = log
		command.Stderr = log
		if err := command.Start(); err != nil {
			_ = log.Close()
			return err
		}
		if err := command.Process.Release(); err != nil {
			_ = log.Close()
			return fmt.Errorf("release daemon process: %w", err)
		}
		return log.Close()
	}
}

// Status requests daemon status without project state.
func (c Client) Status(ctx context.Context) (json.RawMessage, error) {
	return c.Call(ctx, "daemon.status", struct{}{}, nil)
}

// Stop requests clean daemon shutdown.
func (c Client) Stop(ctx context.Context) error {
	_, err := c.Call(ctx, "daemon.stop", struct{}{}, nil)
	return err
}

func requestID() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("create daemon request ID: %w", err)
	}
	return hex.EncodeToString(data), nil
}
