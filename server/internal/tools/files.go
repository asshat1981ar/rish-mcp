package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/turin-dev/rish-mcp/server/internal/mcp"
)

var b64Charset = regexp.MustCompile(`^[A-Za-z0-9+/=]*$`)

// registerFiles adds file_pull and file_push: chunked base64 file transfer
// through the existing exec protocol, bounded so neither the device output
// cap (256 KiB) nor the relay cmd cap (64 KiB) is ever exceeded.
func registerFiles(s *mcp.Server, ex Executor, defaultTimeout, maxTimeout time.Duration) {
	s.RegisterTool(mcp.Tool{
		Name: "file_pull",
		Description: "Reads a file from the device and returns its content as base64 text (plus the raw " +
			"byte size). Works for any file the shell user can read: /sdcard/, /data/local/tmp/, app " +
			"world-readable files. Large files transfer in chunks; capped at ~4.5 MiB of file data.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]mcp.Property{
				"path":      {Type: "string", Description: "Absolute device path, e.g. /sdcard/Download/report.txt"},
				"deviceId":  deviceIDProp,
				"timeoutMs": {Type: "number", Description: "Per-chunk timeout in ms"},
			},
			Required: []string{"path"},
		},
		Handler: filePullHandler(ex, defaultTimeout, maxTimeout),
	})

	s.RegisterTool(mcp.Tool{
		Name: "file_push",
		Description: "Writes contentBase64 to a file on the device (decoded on-device). Target must be an " +
			"absolute path the shell user can write: /sdcard/, /data/local/tmp/. Capped at ~1.5 MiB of " +
			"file data; larger transfers should use run_shell with curl/wget.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]mcp.Property{
				"path":          {Type: "string", Description: "Absolute destination path, e.g. /data/local/tmp/script.sh"},
				"contentBase64": {Type: "string", Description: "File content, base64-encoded (standard alphabet)"},
				"deviceId":      deviceIDProp,
				"timeoutMs":     {Type: "number", Description: "Per-chunk timeout in ms"},
			},
			Required: []string{"path", "contentBase64"},
		},
		Handler: filePushHandler(ex, defaultTimeout, maxTimeout),
	})
}

func filePullHandler(ex Executor, defaultTimeout, maxTimeout time.Duration) func(context.Context, json.RawMessage) (mcp.CallResult, error) {
	return func(ctx context.Context, raw json.RawMessage) (mcp.CallResult, error) {
		var args struct {
			Path      string `json:"path"`
			DeviceID  string `json:"deviceId"`
			TimeoutMs int64  `json:"timeoutMs"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return mcp.CallResult{}, err
		}
		if err := validateDevicePath(args.Path); err != nil {
			return mcp.CallResult{}, err
		}
		timeout := clampTimeout(time.Duration(args.TimeoutMs)*time.Millisecond, defaultTimeout, maxTimeout)
		q := shellQuote(args.Path)

		// Encode to a temp file in one exec; report "<size> ok|missing".
		b64 := tmpName("pull") + ".b64"
		prepare := fmt.Sprintf("if [ -r %s ]; then base64 < %s | tr -d '\\n\\r' > %s; echo \"$(wc -c < %s) ok\"; else echo \"0 missing\"; fi",
			q, q, shellQuote(b64), shellQuote(b64))
		res, cerr := execOnce(ctx, ex, args.DeviceID, prepare, timeout)
		if cerr != nil {
			removeBest(ctx, ex, args.DeviceID, b64, timeout)
			return *cerr, nil
		}
		fields := strings.Fields(res.Stdout)
		if len(fields) < 2 || fields[1] != "ok" {
			removeBest(ctx, ex, args.DeviceID, b64, timeout)
			return textResult("cannot read "+args.Path+" (missing, or not readable by shell uid)", true), nil
		}
		size, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil || size <= 0 {
			removeBest(ctx, ex, args.DeviceID, b64, timeout)
			return textResult("cannot read "+args.Path+" (empty or unreadable)", true), nil
		}
		if size > maxPullB64 {
			removeBest(ctx, ex, args.DeviceID, b64, timeout)
			return textResult(fmt.Sprintf("file too large (%d base64 bytes > %d cap)", size, maxPullB64), true), nil
		}
		blob, err := readBlob(ctx, ex, args.DeviceID, b64, size, timeout)
		removeBest(ctx, ex, args.DeviceID, b64, timeout)
		if err != nil {
			return textResult("file transfer failed: "+err.Error(), true), nil
		}
		if int64(len(blob)) != size {
			return textResult(fmt.Sprintf("transfer size mismatch (got %d, want %d)", len(blob), size), true), nil
		}
		header := fmt.Sprintf("path=%s base64Bytes=%d (~%d raw bytes)\n", args.Path, size, size/4*3)
		return textResult(header+blob, false), nil
	}
}
