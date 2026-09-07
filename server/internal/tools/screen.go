package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/turin-dev/rish-mcp/server/internal/mcp"
)

// registerScreenshot captures the screen with screencap and streams the PNG
// back in base64 chunks (a full-screen PNG exceeds the agent's 256 KiB
// per-exec output cap, so a single-shot read is not possible).
func registerScreenshot(s *mcp.Server, ex Executor, defaultTimeout, maxTimeout time.Duration) {
	s.RegisterTool(mcp.Tool{
		Name: "screenshot",
		Description: "Captures the device screen and returns it as a PNG image. The relay runs screencap " +
			"on-device and transfers the image in base64 chunks over several round trips, so this is " +
			"slower than run_shell — pair it with ui_dump for reliable UI automation.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]mcp.Property{
				"deviceId":  {Type: "string", Description: "Target device id; optional when exactly one device is connected"},
				"timeoutMs": {Type: "number", Description: "Per-chunk timeout in ms"},
			},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (mcp.CallResult, error) {
			var args struct {
				DeviceID  string `json:"deviceId"`
				TimeoutMs int64  `json:"timeoutMs"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return mcp.CallResult{}, err
			}
			timeout := clampTimeout(time.Duration(args.TimeoutMs)*time.Millisecond, defaultTimeout, maxTimeout)

			png := tmpName("shot") + ".png"
			b64 := png + ".b64"
			// One exec: capture, encode (strip line wraps so chunks split at
			// arbitrary byte offsets), drop the PNG, report the b64 size.
			capture := fmt.Sprintf(
				"screencap -p %s && base64 < %s | tr -d '\\n\\r' > %s; rm -f %s; wc -c < %s",
				shellQuote(png), shellQuote(png), shellQuote(b64), shellQuote(png), shellQuote(b64))
			res, cerr := execOnce(ctx, ex, args.DeviceID, capture, timeout)
			if cerr != nil {
				removeBest(ctx, ex, args.DeviceID, b64, timeout)
				return *cerr, nil
			}
			size, err := strconv.ParseInt(strings.TrimSpace(res.Stdout), 10, 64)
			if err != nil || size <= 0 {
				removeBest(ctx, ex, args.DeviceID, b64, timeout)
				detail := strings.TrimSpace(res.Stderr)
				if detail == "" {
					detail = "screencap produced no output"
				}
				return textResult("screenshot failed: "+detail, true), nil
			}
			if size > maxPullB64 {
				removeBest(ctx, ex, args.DeviceID, b64, timeout)
				return textResult(fmt.Sprintf("screenshot too large (%d base64 bytes > %d cap)", size, maxPullB64), true), nil
			}
			blob, err := readBlob(ctx, ex, args.DeviceID, b64, size, timeout)
			removeBest(ctx, ex, args.DeviceID, b64, timeout)
			if err != nil {
				return textResult("screenshot transfer failed: "+err.Error(), true), nil
			}
			if int64(len(blob)) != size {
				return textResult(fmt.Sprintf("screenshot transfer size mismatch (got %d, want %d)", len(blob), size), true), nil
			}
			return mcp.CallResult{Content: []mcp.ContentItem{
				{Type: "image", Data: blob, MimeType: "image/png"},
				{Type: "text", Text: fmt.Sprintf("screenshot: %d base64 bytes (PNG)", size)},
			}}, nil
		},
	})
}

// registerUIDump exposes the uiautomator accessibility tree (element bounds,
// text, resource-ids) so an AI can locate tap targets without guessing.
func registerUIDump(s *mcp.Server, ex Executor, defaultTimeout, maxTimeout time.Duration) {
	s.RegisterTool(mcp.Tool{
		Name: "ui_dump",
		Description: "Dumps the current screen's UI hierarchy (accessibility tree) as XML via uiautomator. " +
			"Nodes carry bounds, text, content-desc, resource-id and clickable/scrollable flags — use it " +
			"to find element coordinates for tap/swipe instead of guessing from a screenshot alone.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]mcp.Property{
				"deviceId":  {Type: "string", Description: "Target device id; optional when exactly one device is connected"},
				"timeoutMs": {Type: "number", Description: "Per-call timeout in ms (uiautomator can take a few seconds)"},
			},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (mcp.CallResult, error) {
			var args struct {
				DeviceID  string `json:"deviceId"`
				TimeoutMs int64  `json:"timeoutMs"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return mcp.CallResult{}, err
			}
			timeout := clampTimeout(time.Duration(args.TimeoutMs)*time.Millisecond, defaultTimeout, maxTimeout)
			// Dump to a temp file then cat: "uiautomator dump /dev/tty" is
			// unreliable across ROMs, while dump-to-file + cat is universal.
			tmp := tmpName("ui") + ".xml"
			cmd := fmt.Sprintf("uiautomator dump %s >/dev/null 2>&1; cat %s; rm -f %s",
				shellQuote(tmp), shellQuote(tmp), shellQuote(tmp))
			res, cerr := execOnce(ctx, ex, args.DeviceID, cmd, timeout)
			if cerr != nil {
				return *cerr, nil
			}
			body := strings.TrimSpace(res.Stdout)
			if body == "" {
				msg := "no UI hierarchy returned (screen off, secure window like a payment screen, or uiautomator unavailable)"
				if detail := strings.TrimSpace(res.Stderr); detail != "" {
					msg += "\nstderr: " + detail
				}
				return textResult(msg, true), nil
			}
			if res.Truncated {
				body += "\n[hierarchy truncated at the agent's 256 KiB output cap]"
			}
			return textResult(body, false), nil
		},
	})
}

var errInvalidCoord = errors.New("coordinates must be integers in 0..32767")

func checkCoord(v int64) error {
	if v < 0 || v > 32767 {
		return errInvalidCoord
	}
	return nil
}
