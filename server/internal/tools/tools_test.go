package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/turin-dev/rish-mcp/server/internal/mcp"
	"github.com/turin-dev/rish-mcp/server/internal/relay"
)

// fakeExecutor routes commands to canned responses by substring match, in
// order; the first matching rule wins. Unmatched commands record the call and
// return success with empty output.
type rule struct {
	contains string
	res      relay.Result
	err      error
}

type fakeExecutor struct {
	rules []rule
	calls []string
}

func (f *fakeExecutor) Exec(_ context.Context, deviceID, cmd string, _ time.Duration) (relay.Result, error) {
	f.calls = append(f.calls, cmd)
	for _, r := range f.rules {
		if strings.Contains(cmd, r.contains) {
			return r.res, r.err
		}
	}
	return relay.Result{Code: 0}, nil
}

func newServerWith(t *testing.T, ex Executor) *mcp.Server {
	t.Helper()
	s := mcp.NewServer("test", "0.0.0")
	Register(s, ex, 5*time.Second, 30*time.Second)
	return s
}

// call invokes a tool by name through the MCP server's JSON-RPC surface and
// returns the CallResult.
func call(t *testing.T, s *mcp.Server, name string, args map[string]any) mcp.CallResult {
	t.Helper()
	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	reqJSON, _ := json.Marshal(map[string]any{
		"name": name, "arguments": json.RawMessage(argsJSON),
	})
	resp := s.Handle(context.Background(), mcp.Request{
		JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/call", Params: reqJSON,
	})
	if resp == nil {
		t.Fatalf("nil response for %s", name)
	}
	if resp.Error != nil {
		t.Fatalf("rpc error for %s: %+v", name, resp.Error)
	}
	b, _ := json.Marshal(resp.Result)
	var out mcp.CallResult
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode result: %v (%s)", err, b)
	}
	return out
}

func resultText(r mcp.CallResult) string {
	var sb strings.Builder
	for _, c := range r.Content {
		sb.WriteString(c.Text)
	}
	return sb.String()
}

func TestRegisterAddsAllTools(t *testing.T) {
	s := newServerWith(t, &fakeExecutor{})
	resp := s.Handle(context.Background(), mcp.Request{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/list"})
	b, _ := json.Marshal(resp.Result)
	want := []string{"run_shell_wont_be_here", "device_info", "screenshot", "ui_dump", "tap", "swipe",
		"key_event", "type_text", "file_pull", "file_push", "list_apps", "logcat_tail"}
	for _, name := range want[1:] {
		if !strings.Contains(string(b), `"`+name+`"`) {
			t.Fatalf("tools/list missing %s", name)
		}
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"plain":      `'plain'`,
		"with space": `'with space'`,
		"it's":       `'it'\''s'`,
		"$(evil)":    `'$(evil)'`,
		"`rm -rf /`": "'`rm -rf /`'",
		"":           `''`,
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidateDevicePath(t *testing.T) {
	bad := []string{"", "relative/path", "/sdcard/../data/x", "/ok\x00bad"}
	for _, p := range bad {
		if err := validateDevicePath(p); err == nil {
			t.Errorf("validateDevicePath(%q) accepted", p)
		}
	}
	good := []string{"/sdcard/a.txt", "/data/local/tmp/x", "/storage/emulated/0/Download/f (1).zip"}
	for _, p := range good {
		if err := validateDevicePath(p); err != nil {
			t.Errorf("validateDevicePath(%q) rejected: %v", p, err)
		}
	}
}

func TestDeviceInfoParsesKeyValues(t *testing.T) {
	ex := &fakeExecutor{rules: []rule{{contains: "getprop ro.product.model", res: relay.Result{Code: 0, Stdout: `model=Pixel 8
manufacturer=Google
release=14
sdk=34
batteryLevel=87
screen=Physical size: 1080x2400
=orphan
no-equals-line
`}}}}
	s := newServerWith(t, ex)
	r := call(t, s, "device_info", map[string]any{})
	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	var info map[string]string
	if err := json.Unmarshal([]byte(resultText(r)), &info); err != nil {
		t.Fatalf("device_info not valid JSON: %v (%s)", err, resultText(r))
	}
	if info["model"] != "Pixel 8" || info["batteryLevel"] != "87" {
		t.Fatalf("bad parse: %+v", info)
	}
	if _, ok := info[""]; ok {
		t.Fatal("empty key leaked into result")
	}
}

func TestDeviceInfoExecError(t *testing.T) {
	ex := &fakeExecutor{rules: []rule{{contains: "getprop", err: relay.ErrNoDevice}}}
	s := newServerWith(t, ex)
	r := call(t, s, "device_info", map[string]any{})
	if !r.IsError || !strings.Contains(resultText(r), "no device") {
		t.Fatalf("expected no-device error, got %+v", r)
	}
}
