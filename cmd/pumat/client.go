package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/chaeeundad/PFCN/internal/agent"
)

// client talks to the local agent over its Unix socket.
type client struct {
	http *http.Client
}

func newClient() *client {
	sock := agent.Paths{Home: home()}.Socket()
	return &client{http: &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		},
	}}}
}

func (c *client) do(method, path string, body io.Reader, out any) error {
	req, err := http.NewRequest(method, "http://agent"+path, body)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		var opErr *net.OpError
		if errors.As(err, &opErr) {
			return fmt.Errorf("cannot reach the local agent at %s: is `pumat agent` running?", agent.Paths{Home: home()}.Socket())
		}
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			return errors.New(e.Error)
		}
		return fmt.Errorf("agent returned %s", resp.Status)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (c *client) get(path string, out any) error { return c.do("GET", path, nil, out) }

func (c *client) post(path string, in, out any) error {
	var body io.Reader
	switch v := in.(type) {
	case nil:
	case []byte:
		body = bytes.NewReader(v)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	return c.do("POST", path, body, out)
}

// submit streams NDJSON progress lines.
func (c *client) submit(req agent.SubmitRequest, progress func(string)) (*agent.SubmitResult, error) {
	b, _ := json.Marshal(req)
	r, err := http.NewRequest("POST", "http://agent/v1/submit", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(r)
	if err != nil {
		return nil, fmt.Errorf("cannot reach the local agent: is `pumat agent` running? (%w)", err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var line struct {
			Progress string              `json:"progress"`
			Error    string              `json:"error"`
			Result   *agent.SubmitResult `json:"result"`
		}
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			return nil, fmt.Errorf("bad agent response: %s", strings.TrimSpace(sc.Text()))
		}
		switch {
		case line.Error != "":
			return nil, errors.New(line.Error)
		case line.Result != nil:
			return line.Result, nil
		case line.Progress != "":
			progress(line.Progress)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("agent closed the connection without a result")
}

func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}
