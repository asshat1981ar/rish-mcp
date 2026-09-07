package tools

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/turin-dev/rish-mcp/server/internal/mcp"
)

// deviceInfoCmd collects a device snapshot in one exec, using shell-side key=
// markers so the relay can parse without brittle line-position assumptions.
const deviceInfoCmd = `echo "model=$(getprop ro.product.model)"
echo "manufacturer=$(getprop ro.product.manufacturer)"
echo "brand=$(getprop ro.product.brand)"
echo "device=$(getprop ro.product.device)"
echo "release=$(getprop ro.build.version.release)"
echo "sdk=$(getprop ro.build.version.sdk)"
echo "buildId=$(getprop ro.build.id)"
echo "screen=$(wm size 2>/dev/null | tail -1)"
echo "density=$(wm density 2>/dev/null | tail -1)"
echo "batteryLevel=$(dumpsys battery 2>/dev/null | awk -F': *' '/level/{print $2; exit}')"
echo "batteryStatus=$(dumpsys battery 2>/dev/null | awk -F': *' '/status/{print $2; exit}')"
echo "uptime=$(cut -d. -f1 /proc/uptime 2>/dev/null)"
echo "storageData=$(df -h /data 2>/dev/null | tail -1 | awk '{print $4" free of "$2}')"
echo "locale=$(getprop persist.sys.locale)"`

func registerDeviceInfo(s *mcp.Server, ex Executor, defaultTimeout, maxTimeout time.Duration) {
	s.RegisterTool(mcp.Tool{
		Name: "device_info",
		Description: "Returns a structured snapshot of a connected Android device: model, manufacturer, " +
			"Android version/SDK, screen size and density, battery level and status, uptime, free storage " +
			"and locale. Uses one round trip. Optional deviceId when multiple devices are connected.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]mcp.Property{
				"deviceId":  {Type: "string", Description: "Target device id; optional when exactly one device is connected"},
				"timeoutMs": {Type: "number", Description: "Per-call timeout in ms"},
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
			res, cerr := execOnce(ctx, ex, args.DeviceID, deviceInfoCmd, timeout)
			if cerr != nil {
				return *cerr, nil
			}
			info := parseKeyValueLines(res.Stdout)
			b, err := json.MarshalIndent(info, "", "  ")
			if err != nil {
				return mcp.CallResult{}, err
			}
			if res.Code != 0 {
				return textResult(string(b)+"\n(exit="+strconv.Itoa(res.Code)+")", true), nil
			}
			return textResult(string(b), false), nil
		},
	})
}

// parseKeyValueLines turns "key=value" lines into a map, keeping the last
// occurrence of any duplicate key and skipping blank/marker noise lines.
func parseKeyValueLines(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, "\r")
		i := strings.IndexByte(line, '=')
		if i <= 0 {
			continue
		}
		k := strings.TrimSpace(line[:i])
		if k == "" || strings.ContainsAny(k, " \t") {
			continue
		}
		out[k] = strings.TrimSpace(line[i+1:])
	}
	return out
}
