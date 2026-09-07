// Package tools registers rish-mcp's device-control tool pack on an MCP
// server: structured device info, screen capture, UI automation, file
// transfer, app listing and log tailing — plus prompt templates ("skills")
// that teach AI clients how to chain the tools into workflows.
//
// Every tool is a hardened wrapper around the existing relay exec protocol
// (one shell command per exec frame, uid 2000 on-device), so the pack works
// with already-deployed Android agents — no app or protocol changes needed.
//
// Size limits honored (see internal/relay and the agent's ShellV2Protocol):
//   - device stdout per exec: 256 KiB (agent-side CappedBuffer)
//   - command string: 64 KiB (relay maxCmdLen)
//   - WebSocket frame: 1 MiB (relay maxReadLimit)
//
// Payloads larger than one exec's output cap (screenshots, file pulls) are
// transferred in chunks: the device writes a base64 blob to /data/local/tmp,
// the relay reads it with tail/head slices, and the temp file is removed
// afterwards.
package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/turin-dev/rish-mcp/server/internal/mcp"
	"github.com/turin-dev/rish-mcp/server/internal/relay"
)

// Executor is the slice of relay.Registry the tools need; keeping it an
// interface makes the handlers unit-testable without a live WebSocket device.
type Executor interface {
	Exec(ctx context.Context, deviceID, cmd string, timeout time.Duration) (relay.Result, error)
}

// Chunking constants, derived from the protocol limits documented above.
const (
	// readChunk is how much base64 text one "tail -c +N | head -c M" exec may
	// return. Half the agent's 256 KiB output cap leaves headroom for shell
	// noise and keeps the WS result frame small.
	readChunk = 128 << 10
	// writeChunk is how much base64 one file_push exec may embed. The relay
	// caps cmd strings at 64 KiB; 48 KiB leaves room for the surrounding
	// printf/quoting syntax.
	writeChunk = 48 << 10
	// maxPullB64 caps total base64 bytes for screenshot/file_pull (~6 MiB of
	// base64 ≈ 4.5 MiB of binary), bounding the number of chunk execs.
	maxPullB64 = 6 << 20
	// maxPushB64 caps file_push input (~1.5 MiB binary).
	maxPushB64 = 2 << 20
	// maxTextLen caps free-form text arguments (type_text, filters).
	maxTextLen = 512
)

// Register wires every tool and prompt in the pack onto s.
func Register(s *mcp.Server, ex Executor, defaultTimeout, maxTimeout time.Duration) {
	registerDeviceInfo(s, ex, defaultTimeout, maxTimeout)
	registerScreenshot(s, ex, defaultTimeout, maxTimeout)
	registerUIDump(s, ex, defaultTimeout, maxTimeout)
	registerInput(s, ex, defaultTimeout, maxTimeout)
	registerFiles(s, ex, defaultTimeout, maxTimeout)
	registerApps(s, ex, defaultTimeout, maxTimeout)
	registerLogcat(s, ex, defaultTimeout, maxTimeout)
	RegisterPrompts(s)
}

// clampTimeout mirrors run_shell's timeout policy so every tool shares it.
func clampTimeout(requested time.Duration, defaultTimeout, maxTimeout time.Duration) time.Duration {
	if requested <= 0 {
		return defaultTimeout
	}
	if requested > maxTimeout {
		return maxTimeout
	}
	return requested
}

// shellQuote wraps s in single quotes for the device's /system/bin/sh,
// escaping embedded single quotes. Every user-controlled string that ends up
// in a command line MUST go through this.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// validateDevicePath rejects relative paths, traversal, NUL bytes and
// whitespace-free violations. Paths are additionally shell-quoted before use.
func validateDevicePath(p string) error {
	if p == "" {
		return errors.New("path is required")
	}
	if !strings.HasPrefix(p, "/") {
		return errors.New("path must be absolute (start with /)")
	}
	if strings.Contains(p, "..") {
		return errors.New("path must not contain '..'")
	}
	if strings.ContainsRune(p, 0) {
		return errors.New("path must not contain NUL")
	}
	if len(p) > 4096 {
		return errors.New("path is too long")
	}
	return nil
}

// tmpName returns a randomized /data/local/tmp path prefix for intermediate
// transfer files, so concurrent calls never collide.
func tmpName(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "/data/local/tmp/.rishmcp_" + prefix + "_" + hex.EncodeToString(b)
}

func textResult(text string, isError bool) mcp.CallResult {
	return mcp.CallResult{Content: []mcp.ContentItem{{Type: "text", Text: text}}, IsError: isError}
}

func errResult(err error) mcp.CallResult {
	return textResult("error: "+err.Error(), true)
}

// execOnce runs one command and converts relay-level failures into MCP error
// results the same way run_shell does.
func execOnce(ctx context.Context, ex Executor, deviceID, cmd string, timeout time.Duration) (relay.Result, *mcp.CallResult) {
	res, err := ex.Exec(ctx, deviceID, cmd, timeout)
	if err != nil {
		out := errResult(err)
		return res, &out
	}
	return res, nil
}

// readBlob reads size bytes of base64 text from a device file in chunks.
func readBlob(ctx context.Context, ex Executor, deviceID, remotePath string, size int64, timeout time.Duration) (string, error) {
	var sb strings.Builder
	sb.Grow(int(size))
	for off := int64(0); off < size; off += readChunk {
		n := int64(readChunk)
		if size-off < n {
			n = size - off
		}
		cmd := fmt.Sprintf("tail -c +%d %s | head -c %d", off+1, shellQuote(remotePath), n)
		res, err := ex.Exec(ctx, deviceID, cmd, timeout)
		if err != nil {
			return "", err
		}
		if res.Truncated {
			return "", errors.New("device truncated a chunk; file changed mid-transfer?")
		}
		sb.WriteString(strings.TrimRight(res.Stdout, "\r\n"))
	}
	return sb.String(), nil
}

// removeBest cleans up a temp file, ignoring errors (best effort).
func removeBest(ctx context.Context, ex Executor, deviceID, path string, timeout time.Duration) {
	_, _ = ex.Exec(ctx, deviceID, "rm -f "+shellQuote(path), timeout)
}
