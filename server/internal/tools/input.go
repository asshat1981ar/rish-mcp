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

// inputArg is the common argument shape of the screen-input tools.
type inputArg struct {
	DeviceID   string `json:"deviceId"`
	X          int64  `json:"x"`
	Y          int64  `json:"y"`
	X2         int64  `json:"x2"`
	Y2         int64  `json:"y2"`
	DurationMs int64  `json:"durationMs"`
	Keycode    string `json:"keycode"`
	Text       string `json:"text"`
	TimeoutMs  int64  `json:"timeoutMs"`
}

// runInput executes a prepared "input ..." command. The on-device input tool
// runs as uid 2000 and can inject events into any app.
func runInput(ctx context.Context, ex Executor, args inputArg, cmd string, defaultTimeout, maxTimeout time.Duration) (mcp.CallResult, error) {
	timeout := clampTimeout(time.Duration(args.TimeoutMs)*time.Millisecond, defaultTimeout, maxTimeout)
	res, cerr := execOnce(ctx, ex, args.DeviceID, cmd, timeout)
	if cerr != nil {
		return *cerr, nil
	}
	body := "ok"
	if strings.TrimSpace(res.Stdout) != "" {
		body = strings.TrimSpace(res.Stdout)
	}
	if res.Code != 0 {
		if detail := strings.TrimSpace(res.Stderr); detail != "" {
			body += "\n" + detail
		}
		return textResult(body, true), nil
	}
	return textResult(body+" ("+cmd+")", false), nil
}

var deviceIDProp = mcp.Property{Type: "string", Description: "Target device id; optional when exactly one device is connected"}

func registerInput(s *mcp.Server, ex Executor, defaultTimeout, maxTimeout time.Duration) {
	s.RegisterTool(mcp.Tool{
		Name: "tap",
		Description: "Taps the device screen at (x, y) pixel coordinates, like a finger touch. Get " +
			"coordinates from ui_dump node bounds or a screenshot. Works as shell uid, so it can " +
			"interact with any foreground app.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]mcp.Property{
				"x":         {Type: "number", Description: "X coordinate in pixels (0..32767)"},
				"y":         {Type: "number", Description: "Y coordinate in pixels (0..32767)"},
				"deviceId":  deviceIDProp,
				"timeoutMs": {Type: "number", Description: "Per-call timeout in ms"},
			},
			Required: []string{"x", "y"},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (mcp.CallResult, error) {
			var args inputArg
			if err := json.Unmarshal(raw, &args); err != nil {
				return mcp.CallResult{}, err
			}
			if err := checkCoord(args.X); err != nil {
				return mcp.CallResult{}, err
			}
			if err := checkCoord(args.Y); err != nil {
				return mcp.CallResult{}, err
			}
			cmd := "input tap " + strconv.FormatInt(args.X, 10) + " " + strconv.FormatInt(args.Y, 10)
			return runInput(ctx, ex, args, cmd, defaultTimeout, maxTimeout)
		},
	})

	s.RegisterTool(mcp.Tool{
		Name: "swipe",
		Description: "Swipes from (x,y) to (x2,y2) over durationMs (default 300). Use for scrolling " +
			"(swipe up = scroll content down), drawers, and gesture navigation.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]mcp.Property{
				"x":          {Type: "number", Description: "Start X in pixels"},
				"y":          {Type: "number", Description: "Start Y in pixels"},
				"x2":         {Type: "number", Description: "End X in pixels"},
				"y2":         {Type: "number", Description: "End Y in pixels"},
				"durationMs": {Type: "number", Description: "Gesture duration in ms (default 300; longer = drag, shorter = fling)"},
				"deviceId":   deviceIDProp,
				"timeoutMs":  {Type: "number", Description: "Per-call timeout in ms"},
			},
			Required: []string{"x", "y", "x2", "y2"},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (mcp.CallResult, error) {
			var args inputArg
			if err := json.Unmarshal(raw, &args); err != nil {
				return mcp.CallResult{}, err
			}
			for _, v := range []int64{args.X, args.Y, args.X2, args.Y2} {
				if err := checkCoord(v); err != nil {
					return mcp.CallResult{}, err
				}
			}
			dur := args.DurationMs
			if dur <= 0 {
				dur = 300
			}
			if dur > 10000 {
				return mcp.CallResult{}, errors.New("durationMs must be <= 10000")
			}
			cmd := "input swipe " + strconv.FormatInt(args.X, 10) + " " + strconv.FormatInt(args.Y, 10) +
				" " + strconv.FormatInt(args.X2, 10) + " " + strconv.FormatInt(args.Y2, 10) +
				" " + strconv.FormatInt(dur, 10)
			return runInput(ctx, ex, args, cmd, defaultTimeout, maxTimeout)
		},
	})
	registerKeyEvent(s, ex, defaultTimeout, maxTimeout)
	registerTypeText(s, ex, defaultTimeout, maxTimeout)
}
