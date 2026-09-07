package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/turin-dev/rish-mcp/server/internal/mcp"
	"github.com/turin-dev/rish-mcp/server/internal/relay"
)

func TestScreenshotChunkedTransfer(t *testing.T) {
	// A tiny "PNG": base64 of it fits in one chunk.
	raw := []byte("fake-png-bytes")
	b64 := base64.StdEncoding.EncodeToString(raw)
	ex := &fakeExecutor{rules: []rule{
		{contains: "screencap", res: relay.Result{Code: 0, Stdout: fmt.Sprintf("%d\n", len(b64))}},
		{contains: "tail -c +", res: relay.Result{Code: 0, Stdout: b64}},
	}}
	s := newServerWith(t, ex)
	r := call(t, s, "screenshot", map[string]any{})
	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	if len(r.Content) != 2 || r.Content[0].Type != "image" || r.Content[0].MimeType != "image/png" {
		t.Fatalf("expected image content item, got %+v", r.Content)
	}
	if r.Content[0].Data != b64 {
		t.Fatalf("image data mismatch: %q", r.Content[0].Data)
	}
	sawCleanup := false
	for _, c := range ex.calls {
		if strings.HasPrefix(c, "rm -f") {
			sawCleanup = true
		}
	}
	if !sawCleanup {
		t.Fatalf("temp file not cleaned up; calls=%v", ex.calls)
	}
}

func TestScreenshotMultiChunkAssembly(t *testing.T) {
	// 300 KiB of base64 forces three 128 KiB chunk reads.
	blob := strings.Repeat("QUJD", 75_000)
	ex := &fakeExecutor{}
	for off := 0; off < len(blob); off += readChunk {
		end := off + readChunk
		if end > len(blob) {
			end = len(blob)
		}
		ex.rules = append(ex.rules, rule{
			contains: fmt.Sprintf("tail -c +%d", off+1),
			res:      relay.Result{Code: 0, Stdout: blob[off:end]},
		})
	}
	ex.rules = append(ex.rules, rule{contains: "screencap", res: relay.Result{Code: 0, Stdout: fmt.Sprintf("%d", len(blob))}})
	s := newServerWith(t, ex)
	r := call(t, s, "screenshot", map[string]any{})
	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	if r.Content[0].Data != blob {
		t.Fatalf("assembled blob mismatch (got %d chars, want %d)", len(r.Content[0].Data), len(blob))
	}
}

func TestScreenshotCaptureFailure(t *testing.T) {
	ex := &fakeExecutor{rules: []rule{
		{contains: "screencap", res: relay.Result{Code: 0, Stdout: "0\n", Stderr: "screencap: failed"}},
	}}
	s := newServerWith(t, ex)
	r := call(t, s, "screenshot", map[string]any{})
	if !r.IsError || !strings.Contains(resultText(r), "screencap: failed") {
		t.Fatalf("expected capture failure surfaced, got %+v", r)
	}
}

func TestScreenshotTooLarge(t *testing.T) {
	ex := &fakeExecutor{rules: []rule{
		{contains: "screencap", res: relay.Result{Code: 0, Stdout: fmt.Sprintf("%d", maxPullB64+1)}},
	}}
	s := newServerWith(t, ex)
	r := call(t, s, "screenshot", map[string]any{})
	if !r.IsError || !strings.Contains(resultText(r), "too large") {
		t.Fatalf("expected size-cap rejection, got %+v", r)
	}
}

func TestUIDumpEmptyIsError(t *testing.T) {
	ex := &fakeExecutor{rules: []rule{{contains: "uiautomator dump", res: relay.Result{Code: 0, Stdout: ""}}}}
	s := newServerWith(t, ex)
	r := call(t, s, "ui_dump", map[string]any{})
	if !r.IsError || !strings.Contains(resultText(r), "no UI hierarchy") {
		t.Fatalf("expected empty-dump error, got %+v", r)
	}
}

func TestUIDumpReturnsXML(t *testing.T) {
	xml := `<hierarchy rotation="0"><node text="Settings" bounds="[0,0][100,50]"/></hierarchy>`
	ex := &fakeExecutor{rules: []rule{{contains: "uiautomator dump", res: relay.Result{Code: 0, Stdout: xml + "\n"}}}}
	s := newServerWith(t, ex)
	r := call(t, s, "ui_dump", map[string]any{})
	if r.IsError || strings.TrimSpace(resultText(r)) != xml {
		t.Fatalf("unexpected result: err=%v text=%q", r.IsError, resultText(r))
	}
}

func mustParams(t *testing.T, name string, args map[string]any) json.RawMessage {
	t.Helper()
	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	b, err := json.Marshal(map[string]any{"name": name, "arguments": json.RawMessage(argsJSON)})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return b
}

func rpcCall(t *testing.T, s *mcp.Server, name string, args map[string]any) *mcp.Response {
	t.Helper()
	return s.Handle(context.Background(), mcp.Request{
		JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/call",
		Params: mustParams(t, name, args),
	})
}
