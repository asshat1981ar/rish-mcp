package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/turin-dev/rish-mcp/server/internal/mcp"
)

// validKeycode accepts numeric keycodes or Android KEYCODE_* names.
func validKeycode(k string) bool {
	if k == "" || len(k) > 64 {
		return false
	}
	if _, err := strconv.Atoi(k); err == nil {
		return true
	}
	if !strings.HasPrefix(k, "KEYCODE_") {
		return false
	}
	for _, r := range k {
		if !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}

func registerKeyEvent(s *mcp.Server, ex Executor, defaultTimeout, maxTimeout time.Duration) {
	s.RegisterTool(mcp.Tool{
		Name: "key_event",
		Description: "Sends an Android key event: KEYCODE_HOME, KEYCODE_BACK, KEYCODE_ENTER, KEYCODE_POWER, " +
			"KEYCODE_VOLUME_UP/DOWN, KEYCODE_APP_SWITCH, or a numeric keycode. Use KEYCODE_BACK to leave " +
			"a screen and KEYCODE_HOME to go to the launcher.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]mcp.Property{
				"keycode":   {Type: "string", Description: "Key name (KEYCODE_*) or numeric keycode"},
				"deviceId":  deviceIDProp,
				"timeoutMs": {Type: "number", Description: "Per-call timeout in ms"},
			},
			Required: []string{"keycode"},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (mcp.CallResult, error) {
			var args inputArg
			if err := json.Unmarshal(raw, &args); err != nil {
				return mcp.CallResult{}, err
			}
			if !validKeycode(args.Keycode) {
				return mcp.CallResult{}, errors.New("keycode must be numeric or a KEYCODE_* name")
			}
			return runInput(ctx, ex, args, "input keyevent "+shellQuote(args.Keycode), defaultTimeout, maxTimeout)
		},
	})
}

func registerTypeText(s *mcp.Server, ex Executor, defaultTimeout, maxTimeout time.Duration) {
	s.RegisterTool(mcp.Tool{
		Name: "type_text",
		Description: "Types text into the focused input field via 'input text'. Spaces are sent as %s per " +
			"the input tool's convention. Printable ASCII only; for other content use run_shell.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]mcp.Property{
				"text":      {Type: "string", Description: "Text to type (max 512 chars, printable ASCII)"},
				"deviceId":  deviceIDProp,
				"timeoutMs": {Type: "number", Description: "Per-call timeout in ms"},
			},
			Required: []string{"text"},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (mcp.CallResult, error) {
			var args inputArg
			if err := json.Unmarshal(raw, &args); err != nil {
				return mcp.CallResult{}, err
			}
			if args.Text == "" {
				return mcp.CallResult{}, errors.New("text is required")
			}
			if len(args.Text) > maxTextLen {
				return mcp.CallResult{}, errors.New("text is too long (max 512 chars)")
			}
			for _, r := range args.Text {
				if r < 0x20 || r > 0x7e {
					return mcp.CallResult{}, errors.New("text must be printable ASCII")
				}
			}
			// 'input text' interprets %s as a space; percent signs pass through
			// literally. Shell quoting protects the transport layer.
			encoded := strings.ReplaceAll(args.Text, " ", "%s")
			return runInput(ctx, ex, args, "input text "+shellQuote(encoded), defaultTimeout, maxTimeout)
		},
	})
}
