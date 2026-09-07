package tools

import (
	"strconv"
	"strings"
	"testing"

	"github.com/turin-dev/rish-mcp/server/internal/relay"
)

func TestFilePullPathValidation(t *testing.T) {
	ex := &fakeExecutor{}
	s := newServerWith(t, ex)
	r := call(t, s, "file_pull", map[string]any{"path": "sdcard/x"})
	if !r.IsError || !strings.Contains(resultText(r), "absolute") {
		t.Fatalf("relative path accepted: %+v", r)
	}
	r = call(t, s, "file_pull", map[string]any{"path": "/sdcard/../etc/passwd"})
	if !r.IsError || !strings.Contains(resultText(r), "'..'") {
		t.Fatalf("traversal path accepted: %+v", r)
	}
	// A rejected path must never be turned into a device command.
	if len(ex.calls) != 0 {
		t.Fatalf("invalid paths reached the device: %v", ex.calls)
	}
}

func TestFilePullMissingFile(t *testing.T) {
	ex := &fakeExecutor{rules: []rule{
		{contains: "if [ -r", res: relay.Result{Code: 0, Stdout: "0 missing\n"}},
	}}
	s := newServerWith(t, ex)
	r := call(t, s, "file_pull", map[string]any{"path": "/sdcard/nope.txt"})
	if !r.IsError || !strings.Contains(resultText(r), "cannot read") {
		t.Fatalf("expected missing-file error, got %+v", r)
	}
}

func TestFilePullRoundTrip(t *testing.T) {
	b64 := "aGVsbG8gZGV2aWNlIGZpbGU"
	ex := &fakeExecutor{rules: []rule{
		{contains: "if [ -r", res: relay.Result{Code: 0, Stdout: strconv.Itoa(len(b64)) + " ok\n"}},
		{contains: "tail -c +", res: relay.Result{Code: 0, Stdout: b64}},
	}}
	s := newServerWith(t, ex)
	r := call(t, s, "file_pull", map[string]any{"path": "/sdcard/a.txt"})
	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	text := resultText(r)
	if !strings.Contains(text, b64) || !strings.Contains(text, "path=/sdcard/a.txt") {
		t.Fatalf("unexpected body: %q", text)
	}
}

// TestFilePullSizeMismatch covers the integrity check: if the assembled blob
// is not exactly the size the device reported, the transfer is rejected rather
// than handed back silently corrupt.
func TestFilePullSizeMismatch(t *testing.T) {
	ex := &fakeExecutor{rules: []rule{
		{contains: "if [ -r", res: relay.Result{Code: 0, Stdout: "99 ok\n"}},
		{contains: "tail -c +", res: relay.Result{Code: 0, Stdout: "aGVsbG8"}},
	}}
	s := newServerWith(t, ex)
	r := call(t, s, "file_pull", map[string]any{"path": "/sdcard/a.txt"})
	if !r.IsError || !strings.Contains(resultText(r), "size mismatch") {
		t.Fatalf("expected size-mismatch error, got %+v", r)
	}
}

func TestFilePullTooLarge(t *testing.T) {
	ex := &fakeExecutor{rules: []rule{
		{contains: "if [ -r", res: relay.Result{Code: 0, Stdout: strconv.Itoa(maxPullB64+1) + " ok\n"}},
	}}
	s := newServerWith(t, ex)
	r := call(t, s, "file_pull", map[string]any{"path": "/sdcard/big.bin"})
	if !r.IsError || !strings.Contains(resultText(r), "too large") {
		t.Fatalf("expected size-cap rejection, got %+v", r)
	}
	// Must bail before streaming any chunk.
	for _, c := range ex.calls {
		if strings.Contains(c, "tail -c +") {
			t.Fatalf("oversized file streamed anyway: %q", c)
		}
	}
}

func TestFilePushValidation(t *testing.T) {
	ex := &fakeExecutor{}
	s := newServerWith(t, ex)
	r := call(t, s, "file_push", map[string]any{"path": "/data/local/tmp/x", "contentBase64": "not!base64!"})
	if !r.IsError || !strings.Contains(resultText(r), "base64 alphabet") {
		t.Fatalf("non-base64 content accepted: %+v", r)
	}
	r = call(t, s, "file_push", map[string]any{"path": "/data/local/tmp/x", "contentBase64": ""})
	if !r.IsError || !strings.Contains(resultText(r), "required") {
		t.Fatalf("empty content accepted: %+v", r)
	}
	r = call(t, s, "file_push", map[string]any{"path": "relative", "contentBase64": "YQ=="})
	if !r.IsError || !strings.Contains(resultText(r), "absolute") {
		t.Fatalf("relative path accepted: %+v", r)
	}
	if len(ex.calls) != 0 {
		t.Fatalf("invalid push reached the device: %v", ex.calls)
	}
}

func TestFilePushRoundTrip(t *testing.T) {
	ex := &fakeExecutor{rules: []rule{
		{contains: "base64 -d", res: relay.Result{Code: 0, Stdout: "pushed\n13\n"}},
	}}
	s := newServerWith(t, ex)
	r := call(t, s, "file_push", map[string]any{"path": "/data/local/tmp/x.txt", "contentBase64": "aGVsbG8gZGV2aWNl\nIGZpbGU="})
	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "pushed /data/local/tmp/x.txt") {
		t.Fatalf("unexpected body: %q", resultText(r))
	}
	// Whitespace inside the base64 is stripped, chunk writes are quoted
	// printf appends, and the final step decodes + mv's into place.
	if !strings.Contains(ex.calls[0], "printf %s '") {
		t.Fatalf("first call not a quoted printf append: %q", ex.calls[0])
	}
	if strings.Contains(ex.calls[0], "\n") && !strings.HasPrefix(ex.calls[0], "printf") {
		t.Fatalf("newline leaked into command: %q", ex.calls[0])
	}
	if !strings.Contains(ex.calls[len(ex.calls)-1], "base64 -d") || !strings.Contains(ex.calls[len(ex.calls)-1], " mv ") {
		t.Fatalf("final call missing decode/mv: %q", ex.calls[len(ex.calls)-1])
	}
}

func TestFilePushDecodeFailure(t *testing.T) {
	ex := &fakeExecutor{rules: []rule{
		{contains: "base64 -d", res: relay.Result{Code: 1, Stderr: "base64: invalid input"}},
	}}
	s := newServerWith(t, ex)
	r := call(t, s, "file_push", map[string]any{"path": "/data/local/tmp/x.txt", "contentBase64": "YQ=="})
	if !r.IsError || !strings.Contains(resultText(r), "invalid input") {
		t.Fatalf("expected decode failure surfaced, got %+v", r)
	}
}
