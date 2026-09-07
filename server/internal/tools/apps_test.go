package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/turin-dev/rish-mcp/server/internal/mcp"
	"github.com/turin-dev/rish-mcp/server/internal/relay"
)

func TestListAppsScopesAndFilter(t *testing.T) {
	ex := &fakeExecutor{rules: []rule{{contains: "pm list packages", res: relay.Result{Code: 0, Stdout: "package:com.android.chrome\n"}}}}
	s := newServerWith(t, ex)

	r := call(t, s, "list_apps", map[string]any{})
	if r.IsError || !strings.Contains(resultText(r), "com.android.chrome") {
		t.Fatalf("default scope failed: %+v", r)
	}
	if ex.calls[0] != "pm list packages" {
		t.Fatalf("bad default command: %q", ex.calls[0])
	}

	call(t, s, "list_apps", map[string]any{"scope": "user"})
	if ex.calls[1] != "pm list packages -3" {
		t.Fatalf("bad user-scope command: %q", ex.calls[1])
	}

	call(t, s, "list_apps", map[string]any{"scope": "system"})
	if ex.calls[2] != "pm list packages -s" {
		t.Fatalf("bad system-scope command: %q", ex.calls[2])
	}

	// Filter goes through grep with shell quoting.
	call(t, s, "list_apps", map[string]any{"filter": "chrome"})
	if ex.calls[3] != "pm list packages | grep -F 'chrome'" {
		t.Fatalf("bad filter command: %q", ex.calls[3])
	}

	// Injection via filter must be neutralized by quoting.
	call(t, s, "list_apps", map[string]any{"filter": "x'; rm -rf /; echo '"})
	if !strings.Contains(ex.calls[4], `grep -F 'x'\''; rm -rf /; echo '\'''`) {
		t.Fatalf("filter not safely quoted: %q", ex.calls[4])
	}

	// Invalid scope is rejected without touching the device.
	n := len(ex.calls)
	r = call(t, s, "list_apps", map[string]any{"scope": "bogus"})
	if !r.IsError || len(ex.calls) != n {
		t.Fatalf("invalid scope not rejected: %+v calls=%v", r, ex.calls)
	}

	// Oversized filter rejected.
	r = call(t, s, "list_apps", map[string]any{"filter": strings.Repeat("a", 513)})
	if !r.IsError || len(ex.calls) != n {
		t.Fatalf("oversized filter not rejected: %+v", r)
	}
}

func TestListAppsGrepNoMatchIsNotAnError(t *testing.T) {
	// grep exits 1 with empty output when nothing matches.
	ex := &fakeExecutor{rules: []rule{{contains: "grep", res: relay.Result{Code: 1, Stdout: ""}}}}
	s := newServerWith(t, ex)
	r := call(t, s, "list_apps", map[string]any{"filter": "nonexistent"})
	if r.IsError {
		t.Fatalf("no-match treated as error: %+v", r)
	}
	if !strings.Contains(resultText(r), "no matching packages") {
		t.Fatalf("missing no-match message: %q", resultText(r))
	}
}

func TestLogcatTailDefaultsAndValidation(t *testing.T) {
	ex := &fakeExecutor{rules: []rule{{contains: "logcat", res: relay.Result{Code: 0, Stdout: "01-01 00:00:00.000 E/Tag: crash\n"}}}}
	s := newServerWith(t, ex)

	r := call(t, s, "logcat_tail", map[string]any{})
	if r.IsError || !strings.Contains(resultText(r), "E/Tag: crash") {
		t.Fatalf("default call failed: %+v", r)
	}
	if ex.calls[0] != "logcat -d -v time -t 200" {
		t.Fatalf("bad default command: %q", ex.calls[0])
	}

	// lines clamped to 5000.
	call(t, s, "logcat_tail", map[string]any{"lines": 999999})
	if ex.calls[1] != "logcat -d -v time -t 5000" {
		t.Fatalf("lines not clamped: %q", ex.calls[1])
	}

	// Tag filter with priority.
	call(t, s, "logcat_tail", map[string]any{"tag": "AndroidRuntime", "priority": "E"})
	if ex.calls[2] != "logcat -d -v time -t 200 -s 'AndroidRuntime':E" {
		t.Fatalf("bad tag command: %q", ex.calls[2])
	}

	// Invalid priority rejected.
	n := len(ex.calls)
	for _, bad := range []string{"X", "EE", ""} {
		r := call(t, s, "logcat_tail", map[string]any{"tag": "T", "priority": bad})
		if bad == "" {
			// empty priority defaults to V and must succeed
			if r.IsError {
				t.Fatalf("empty priority should default: %+v", r)
			}
			continue
		}
		if !r.IsError {
			t.Fatalf("priority %q accepted", bad)
		}
	}
	if len(ex.calls) != n+1 { // only the valid empty-priority call ran
		t.Fatalf("unexpected device calls: %v", ex.calls)
	}

	// Tag injection neutralized.
	call(t, s, "logcat_tail", map[string]any{"tag": "x';id;'"})
	if !strings.Contains(ex.calls[len(ex.calls)-1], `'x'\'';id;'\'''`) {
		t.Fatalf("tag not safely quoted: %q", ex.calls[len(ex.calls)-1])
	}
}

func TestPromptsListAndGet(t *testing.T) {
	s := newServerWith(t, &fakeExecutor{})
	RegisterPrompts(s)

	// prompts/list must include all three skills.
	resp := s.Handle(context.Background(), mcp.Request{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "prompts/list"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("prompts/list failed: %+v", resp)
	}
	b, _ := json.Marshal(resp.Result)
	for _, name := range []string{"android_ui_task", "android_device_report", "android_app_debug"} {
		if !strings.Contains(string(b), name) {
			t.Fatalf("prompts/list missing %s: %s", name, b)
		}
	}

	// prompts/get interpolates {{task}}.
	params, _ := json.Marshal(map[string]any{"name": "android_ui_task", "arguments": map[string]any{"task": "enable dark theme"}})
	resp = s.Handle(context.Background(), mcp.Request{JSONRPC: "2.0", ID: json.RawMessage(`2`), Method: "prompts/get", Params: params})
	if resp == nil || resp.Error != nil {
		t.Fatalf("prompts/get failed: %+v", resp)
	}
	b, _ = json.Marshal(resp.Result)
	if !strings.Contains(string(b), "enable dark theme") {
		t.Fatalf("argument not interpolated: %s", b)
	}
	if strings.Contains(string(b), "{{task}}") {
		t.Fatalf("placeholder left unsubstituted: %s", b)
	}

	// Unknown prompt → JSON-RPC error.
	params, _ = json.Marshal(map[string]any{"name": "nope"})
	resp = s.Handle(context.Background(), mcp.Request{JSONRPC: "2.0", ID: json.RawMessage(`3`), Method: "prompts/get", Params: params})
	if resp == nil || resp.Error == nil {
		t.Fatalf("unknown prompt should error: %+v", resp)
	}
}
