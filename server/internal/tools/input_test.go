package tools

import (
	"strings"
	"testing"

	"github.com/turin-dev/rish-mcp/server/internal/relay"
)

func TestTapBuildsInputCommand(t *testing.T) {
	ex := &fakeExecutor{}
	s := newServerWith(t, ex)
	r := call(t, s, "tap", map[string]any{"x": 540, "y": 1200})
	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	if len(ex.calls) != 1 || ex.calls[0] != "input tap 540 1200" {
		t.Fatalf("bad command: %v", ex.calls)
	}
}

func TestTapRejectsBadCoordinates(t *testing.T) {
	ex := &fakeExecutor{}
	s := newServerWith(t, ex)
	for _, args := range []map[string]any{
		{"x": -1, "y": 10},
		{"x": 10, "y": -1},
		{"x": 32768, "y": 10},
		{"x": 10, "y": 40000},
	} {
		r := call(t, s, "tap", args)
		if !r.IsError {
			t.Fatalf("tap accepted %+v", args)
		}
	}
	if len(ex.calls) != 0 {
		t.Fatalf("rejected coordinates reached the device: %v", ex.calls)
	}
}

func TestSwipeDefaultsAndValidatesDuration(t *testing.T) {
	ex := &fakeExecutor{}
	s := newServerWith(t, ex)
	r := call(t, s, "swipe", map[string]any{"x": 540, "y": 1600, "x2": 540, "y2": 600})
	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	if ex.calls[0] != "input swipe 540 1600 540 600 300" {
		t.Fatalf("bad default-duration command: %q", ex.calls[0])
	}

	r = call(t, s, "swipe", map[string]any{"x": 0, "y": 0, "x2": 10, "y2": 10, "durationMs": 10001})
	if !r.IsError {
		t.Fatal("swipe accepted durationMs > 10000")
	}
	if len(ex.calls) != 1 {
		t.Fatalf("rejected duration reached the device: %v", ex.calls)
	}

	r = call(t, s, "swipe", map[string]any{"x": 0, "y": 0, "x2": 10, "y2": 10, "durationMs": 450})
	if r.IsError || !strings.HasSuffix(ex.calls[1], "450") {
		t.Fatalf("explicit duration not honored: %v", ex.calls)
	}
}

func TestKeyEventValidation(t *testing.T) {
	ex := &fakeExecutor{}
	s := newServerWith(t, ex)

	r := call(t, s, "key_event", map[string]any{"keycode": "KEYCODE_HOME"})
	if r.IsError || ex.calls[0] != "input keyevent 'KEYCODE_HOME'" {
		t.Fatalf("named keycode failed: %v %v", r, ex.calls)
	}

	r = call(t, s, "key_event", map[string]any{"keycode": "66"})
	if r.IsError || !strings.Contains(ex.calls[1], "66") {
		t.Fatalf("numeric keycode failed: %v %v", r, ex.calls)
	}

	// Injection attempts must be rejected before reaching the shell.
	for _, bad := range []string{"", "HOME", "KEYCODE_BACK;rm -rf /", "KEYCODE_$(evil)", "`id`", "4;id", strings.Repeat("K", 65)} {
		r := call(t, s, "key_event", map[string]any{"keycode": bad})
		if !r.IsError {
			t.Fatalf("key_event accepted %q", bad)
		}
	}
	if len(ex.calls) != 2 {
		t.Fatalf("rejected keycodes reached the device: %v", ex.calls)
	}
}

func TestTypeTextEncodingAndValidation(t *testing.T) {
	ex := &fakeExecutor{}
	s := newServerWith(t, ex)

	r := call(t, s, "type_text", map[string]any{"text": "hello world 42"})
	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	if ex.calls[0] != "input text 'hello%sworld%s42'" {
		t.Fatalf("spaces not encoded per input(1) convention: %q", ex.calls[0])
	}

	// Shell metacharacters must survive inside single quotes, not execute.
	r = call(t, s, "type_text", map[string]any{"text": "a'b;$(id)"})
	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	want := `input text 'a'\''b;$(id)'`
	if ex.calls[1] != want {
		t.Fatalf("quote escaping wrong:\n got %q\nwant %q", ex.calls[1], want)
	}

	for _, bad := range []string{"", "héllo", strings.Repeat("x", 513), "a\nb"} {
		if r := call(t, s, "type_text", map[string]any{"text": bad}); !r.IsError {
			t.Fatalf("type_text accepted %q", bad)
		}
	}
	if len(ex.calls) != 2 {
		t.Fatalf("rejected text reached the device: %v", ex.calls)
	}
}

func TestInputExecFailureSurfacesError(t *testing.T) {
	ex := &fakeExecutor{rules: []rule{{contains: "input", err: relay.ErrTimeout}}}
	s := newServerWith(t, ex)
	r := call(t, s, "tap", map[string]any{"x": 1, "y": 1})
	if !r.IsError || !strings.Contains(resultText(r), "timed out") {
		t.Fatalf("expected timeout error, got %+v", r)
	}
}

func TestInputNonZeroExitIncludesStderr(t *testing.T) {
	ex := &fakeExecutor{rules: []rule{{contains: "input", res: relay.Result{Code: 1, Stderr: "INJECT_EVENTS denied"}}}}
	s := newServerWith(t, ex)
	r := call(t, s, "key_event", map[string]any{"keycode": "KEYCODE_BACK"})
	if !r.IsError || !strings.Contains(resultText(r), "INJECT_EVENTS denied") {
		t.Fatalf("stderr not surfaced: %+v", r)
	}
}
