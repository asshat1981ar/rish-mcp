package tools

import "github.com/turin-dev/rish-mcp/server/internal/mcp"

// RegisterPrompts exposes reusable workflow templates ("skills") over MCP's
// prompts capability. Clients list them with prompts/list and fetch the
// instructions with prompts/get; {{arg}} placeholders are interpolated.
func RegisterPrompts(s *mcp.Server) {
	s.RegisterPrompt(mcp.Prompt{
		Name:        "android_ui_task",
		Description: "Step-by-step method for accomplishing a UI task on the connected Android device reliably.",
		Arguments: []mcp.PromptArgument{
			{Name: "task", Description: "What to accomplish on the device, e.g. 'open Settings and enable dark theme'", Required: true},
		},
		Text: `Accomplish this task on the connected Android device: {{task}}

Follow this loop until the task is done:
1. Call list_devices to confirm the device is online (pass deviceId if more than one).
2. Call ui_dump to get the current screen's accessibility tree. Find target elements by
   text, content-desc or resource-id and read their bounds="[x1,y1][x2,y2]".
3. Compute the tap point as the bounds center: ((x1+x2)/2, (y1+y2)/2) and call tap.
   If the tree is unavailable, call screenshot and estimate coordinates from the image.
4. To scroll, call swipe (e.g. from (540,1600) to (540,600) over 400ms scrolls content down).
5. To type, tap the field first, then call type_text. Use key_event with KEYCODE_ENTER to
   submit, KEYCODE_BACK to go back, KEYCODE_HOME for the launcher.
6. After each action, call ui_dump or screenshot again to verify the effect before the
   next step. Never fire more than one action between verifications.
7. If something fails, call logcat_tail with the app's tag to diagnose, then retry.

Safety: you operate a real user device as shell uid. Do not change account, payment or
security settings unless the task explicitly asks for it, and stop to ask if a step is
destructive (uninstall, factory reset, sending messages).`,
	})

	s.RegisterPrompt(mcp.Prompt{
		Name:        "android_device_report",
		Description: "Produce a health/inventory report of the connected Android device.",
		Text: `Produce a concise report about the connected Android device:
1. Call list_devices for connection metadata (kind, SDK, agent version).
2. Call device_info for model, Android version, battery, storage, screen, locale.
3. Call list_apps with scope=user for installed third-party apps (summarize counts and
   notable apps; do not list everything verbatim).
4. Call logcat_tail with lines=100 and priority filter E via run_shell if you need errors:
   run_shell cmd "logcat -d -t 100 *:E".
5. Summarize findings in sections: Device, Battery & Storage, Apps, Recent Errors, and
   flag anything unusual (low storage, crash loops, very old agent version).`,
	})

	s.RegisterPrompt(mcp.Prompt{
		Name:        "android_app_debug",
		Description: "Debug a misbehaving Android app: launch, reproduce, capture logs and crash traces.",
		Arguments: []mcp.PromptArgument{
			{Name: "package", Description: "Package name of the app to debug, e.g. com.example.app", Required: true},
			{Name: "symptom", Description: "What is going wrong", Required: true},
		},
		Text: `Debug the Android app {{package}}. Symptom: {{symptom}}

1. run_shell: "logcat -c" to clear the log buffer.
2. run_shell: "monkey -p {{package}} -c android.intent.category.LAUNCHER 1" to launch it
   (or "am start -n <pkg>/<activity>" if you know the entry activity).
3. Reproduce the symptom using the ui_dump → tap/swipe loop; verify each step.
4. run_shell: "logcat -d -v time -t 500 --pid=$(pidof {{package}})" for app-only logs;
   also check "logcat -d -t 200 AndroidRuntime:E *:F" for crashes/ANRs.
5. If it crashed, pull the tombstone list: run_shell "ls -lt /data/tombstones 2>/dev/null | head".
6. Report: what happened, the relevant log lines (quote them), likely cause, and a
   suggested next diagnostic step.`,
	})
}
