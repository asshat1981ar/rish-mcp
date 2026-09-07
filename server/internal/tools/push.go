package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/turin-dev/rish-mcp/server/internal/mcp"
)

func filePushHandler(ex Executor, defaultTimeout, maxTimeout time.Duration) func(context.Context, json.RawMessage) (mcp.CallResult, error) {
	return func(ctx context.Context, raw json.RawMessage) (mcp.CallResult, error) {
		var args struct {
			Path          string `json:"path"`
			ContentBase64 string `json:"contentBase64"`
			DeviceID      string `json:"deviceId"`
			TimeoutMs     int64  `json:"timeoutMs"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return mcp.CallResult{}, err
		}
		if err := validateDevicePath(args.Path); err != nil {
			return mcp.CallResult{}, err
		}
		blob := strings.NewReplacer("\n", "", "\r", "", " ", "").Replace(args.ContentBase64)
		if blob == "" {
			return mcp.CallResult{}, errors.New("contentBase64 is required")
		}
		if !b64Charset.MatchString(blob) {
			return mcp.CallResult{}, errors.New("contentBase64 contains characters outside the base64 alphabet")
		}
		if len(blob) > maxPushB64 {
			return mcp.CallResult{}, fmt.Errorf("content too large (%d base64 chars > %d cap)", len(blob), maxPushB64)
		}
		timeout := clampTimeout(time.Duration(args.TimeoutMs)*time.Millisecond, defaultTimeout, maxTimeout)

		// Stage base64 in a temp file with printf appends (chunks are validated
		// base64 and shell-quoted anyway), then decode to a second temp and mv
		// over the target only on success — a failed decode never clobbers the
		// destination.
		tmp := tmpName("push")
		first := true
		for off := 0; off < len(blob); off += writeChunk {
			end := off + writeChunk
			if end > len(blob) {
				end = len(blob)
			}
			op := ">>"
			if first {
				op = ">"
				first = false
			}
			cmd := fmt.Sprintf("printf %%s %s %s %s", shellQuote(blob[off:end]), op, shellQuote(tmp+".b64"))
			if _, cerr := execOnce(ctx, ex, args.DeviceID, cmd, timeout); cerr != nil {
				removeBest(ctx, ex, args.DeviceID, tmp+".b64", timeout)
				return *cerr, nil
			}
		}
		decode := fmt.Sprintf("base64 -d < %s > %s && mv %s %s && rm -f %s && echo pushed && wc -c < %s",
			shellQuote(tmp+".b64"), shellQuote(tmp+".out"), shellQuote(tmp+".out"), shellQuote(args.Path),
			shellQuote(tmp+".b64"), shellQuote(args.Path))
		res, cerr := execOnce(ctx, ex, args.DeviceID, decode, timeout)
		if cerr != nil {
			removeBest(ctx, ex, args.DeviceID, tmp+".b64", timeout)
			removeBest(ctx, ex, args.DeviceID, tmp+".out", timeout)
			return *cerr, nil
		}
		if res.Code != 0 || !strings.Contains(res.Stdout, "pushed") {
			detail := strings.TrimSpace(res.Stderr)
			if detail == "" {
				detail = "decode/write failed (bad base64 or unwritable path)"
			}
			removeBest(ctx, ex, args.DeviceID, tmp+".b64", timeout)
			return textResult("file_push failed: "+detail, true), nil
		}
		lines := strings.Split(strings.TrimSpace(res.Stdout), "\n")
		written := strings.TrimSpace(lines[len(lines)-1])
		return textResult(fmt.Sprintf("pushed %s (%s bytes on device)", args.Path, written), false), nil
	}
}
