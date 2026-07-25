// Package herdr is a minimal client for the Herdr socket API.
//
// The socket speaks newline-delimited JSON: write {"id","method","params"},
// read back {"id","result"} or {"id","error"}. Every request uses a
// short-lived connection, which keeps this package free of any lifecycle the
// caller has to manage.
package herdr

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sync/atomic"
	"time"
)

// Client talks to the Herdr server over its unix socket.
type Client struct {
	socketPath string
	seq        atomic.Uint64
}

// New builds a client from HERDR_SOCKET_PATH.
func New() (*Client, error) {
	path := os.Getenv("HERDR_SOCKET_PATH")
	if path == "" {
		return nil, errors.New("HERDR_SOCKET_PATH is not set: run this inside a Herdr session")
	}
	return &Client{socketPath: path}, nil
}

type request struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

type apiErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type response struct {
	ID     string           `json:"id"`
	Result json.RawMessage  `json:"result"`
	Error  *apiErrorPayload `json:"error"`
}

// APIError is a structured error the server sent back, as opposed to a
// transport failure (dial, decode, timeout). Code is exported so a caller can
// tell one kind of rejection from another with errors.As, rather than
// matching against formatted text -- agent.start's "the pane just spawned and
// is not available yet" is meant to be retried; most other errors are not.
type APIError struct {
	Method  string
	Code    string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s: %s (%s)", e.Method, e.Message, e.Code)
}

func (c *Client) nextID(method string) string {
	return fmt.Sprintf("util:%s:%d", method, c.seq.Add(1))
}

// Request performs one request/response round trip.
func (c *Client) Request(method string, params any, out any) error {
	conn, err := net.DialTimeout("unix", c.socketPath, 3*time.Second)
	if err != nil {
		return fmt.Errorf("dial herdr socket: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	payload, err := json.Marshal(request{ID: c.nextID(method), Method: method, Params: params})
	if err != nil {
		return err
	}
	if _, err := conn.Write(append(payload, '\n')); err != nil {
		return fmt.Errorf("write %s: %w", method, err)
	}

	// A session snapshot runs well past bufio.Scanner's 64KiB default, so the
	// buffer is raised rather than risking a truncated read on a busy session.
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 8<<20)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("read %s: %w", method, err)
		}
		return fmt.Errorf("read %s: connection closed with no response", method)
	}

	var resp response
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return fmt.Errorf("decode %s: %w", method, err)
	}
	if resp.Error != nil {
		return &APIError{Method: method, Code: resp.Error.Code, Message: resp.Error.Message}
	}
	if out != nil {
		if err := json.Unmarshal(resp.Result, out); err != nil {
			return fmt.Errorf("decode %s result: %w", method, err)
		}
	}
	return nil
}
