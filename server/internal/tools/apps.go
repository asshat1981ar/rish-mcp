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

func registerApps(s *mcp.Server, ex Executor, defaultTimeout, maxTimeout time.Duration) {
	s.RegisterTool(mcp.Tool{
		Name: "list_apps",
		Description: "Lists installed Android packages. Filter by scope (all/user/system) and an optional " +
			"substring match on the package name. Returns one 'package:<name>' line per app.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]mcp.Property{
				"deviceId":  deviceIDProp,
				"scope":     {Type: "string", Description: "all (default), user (3rd-party only), or system"},
				"filter":    {Type: "string", Description: "Optional case-sensitive substring to match, e.g. 'chrome'"},
				"timeoutMs": {Type: "number", Description: "Per-call timeout in ms"},
			},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (mcp.CallResult, error) {
			var args struct {
				DeviceID  string `json:"deviceId"`
				Scope     string `json:"scope"`
				Filter    string `json:"filter"`
				TimeoutMs int64  `json:"timeoutMs"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return mcp.CallResult{}, err
			}
			scope := args.Scope
			if scope == "" {
				scope = "all"
			}
			var flag string
			switch scope {
			case "all":
			case "user":
				flag = " -3"
			case "system":
				flag = " -s"
			default:
				return mcp.CallResult{}, errors.New("scope must be all, user, or system")
			}
			cmd := "pm list packages" + flag
			if args.Filter != "" {
				if len(args.Filter) > maxTextLen {
					return mcp.CallResult{}, errors.New("filter is too long")
				}
				cmd += " | grep -F " + shellQuote(args.Filter)
			}
			timeout := clampTimeout(time.Duration(args.TimeoutMs)*time.Millisecond, defaultTimeout, maxTimeout)
			res, cerr := execOnce(ctx, ex, args.DeviceID, cmd, timeout)
			if cerr != nil {
				return *cerr, nil
			}
			body := strings.TrimSpace(res.Stdout)
			// grep exits 1 when nothing matches: an empty result, not an error.
			if res.Code == 1 && body == "" {
				return textResult("(no matching packages)", false), nil
			}
			if body == "" && res.Code == 0 {
				body = "(no matching packages)"
			}
			if res.Truncated {
				body += "\n[output truncated]"
			}
			return textResult(body, res.Code != 0), nil
		},
	})
}

func registerLogcat(s *mcp.Server, ex Executor, defaultTimeout, maxTimeout time.Duration) {
	s.RegisterTool(mcp.Tool{
		Name: "logcat_tail",
		Description: "Returns the most recent logcat lines (system/app logs). Useful for diagnosing a " +
			"crash or verifying an action's effect. Optionally filter by tag and minimum priority.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]mcp.Property{
				"deviceId":  deviceIDProp,
				"lines":     {Type: "number", Description: "Max recent lines to return (default 200, capped 5000)"},
				"tag":       {Type: "string", Description: "Optional logcat tag filter, e.g. 'AndroidRuntime'"},
				"priority":  {Type: "string", Description: "Optional minimum priority when using tag: V,D,I,W,E,F (default V)"},
				"timeoutMs": {Type: "number", Description: "Per-call timeout in ms"},
			},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (mcp.CallResult, error) {
			var args struct {
				DeviceID  string `json:"deviceId"`
				Lines     int64  `json:"lines"`
				Tag       string `json:"tag"`
				Priority  string `json:"priority"`
				TimeoutMs int64  `json:"timeoutMs"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return mcp.CallResult{}, err
			}
			lines := args.Lines
			if lines <= 0 {
				lines = 200
			}
			if lines > 5000 {
				lines = 5000
			}
			prio := args.Priority
			if prio == "" {
				prio = "V"
			}
			if len(prio) != 1 || !strings.ContainsRune("VDIWEF", rune(prio[0])) {
				return mcp.CallResult{}, errors.New("priority must be one of V,D,I,W,E,F")
			}
			cmd := "logcat -d -v time -t " + strconv.FormatInt(lines, 10)
			if args.Tag != "" {
				if len(args.Tag) > maxTextLen {
					return mcp.CallResult{}, errors.New("tag is too long")
				}
				cmd += " -s " + shellQuote(args.Tag) + ":" + prio
			}
			timeout := clampTimeout(time.Duration(args.TimeoutMs)*time.Millisecond, defaultTimeout, maxTimeout)
			res, cerr := execOnce(ctx, ex, args.DeviceID, cmd, timeout)
			if cerr != nil {
				return *cerr, nil
			}
			body := res.Stdout
			if res.Truncated {
				body += "\n[output truncated]"
			}
			if strings.TrimSpace(body) == "" && res.Code == 0 {
				body = "(no matching log lines)"
			}
			return textResult(body, res.Code != 0), nil
		},
	})
}
