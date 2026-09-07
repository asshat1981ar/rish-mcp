# Enhancements: research notes & roadmap

This document records the research behind the device-control tool pack
(`server/internal/tools`) and the remaining enhancement ideas, ordered by
value/effort. It complements `DESIGN.md` (what is) and `plan.md` (why).

## Shipped: device-control tool pack

Goal: turn rish-mcp from "a shell over WebSocket" into a practical Android
automation surface for AI clients, **without touching the agent app or the WS
protocol**. Every new tool is a server-side composition of commands the
existing agent already executes as uid 2000.

### Architecture decisions

- **New package `internal/tools`** — `cmd/relay` stays a thin wiring layer.
  Tools take an `Executor` interface (`Exec(ctx, deviceID, cmd, timeout)`),
  satisfied by `*relay.Registry`, which keeps them unit-testable with a fake
  executor and decoupled from transport.
- **Chunked blob transfer** — the agent caps stdout at 256 KiB and the relay
  caps `cmd` at 64 KiB. `screenshot`, `ui_dump`, `file_pull` write a base64
  blob to `/data/local/tmp` on-device and read it back in ~128 KiB `dd`
  chunks; `file_push` stages via quoted `printf '%s'` appends. Size caps:
  ~4.5 MiB pull / ~1.5 MiB push. Transfer integrity is checked against the
  on-device `wc -c` size before returning.
- **Injection safety** — every user string reaching a command line goes
  through `shellQuote` (POSIX single-quote escaping); paths through
  `validateDevicePath` (absolute, no `..`, no NUL); keycodes/coordinates/
  priorities through strict allow-lists. Tests assert rejected input never
  reaches the executor.
- **MCP protocol surgery** (`internal/mcp`) — added image content items
  (`data`+`mimeType`, used by `screenshot`) and the prompts capability
  (`prompts/list`, `prompts/get` with `{{arg}}` interpolation and required-arg
  validation). Still hand-rolled, still stateless, matching the repo's
  accepted risk of avoiding an immature Go SDK.
- **Prompts as "skills"** — three workflow templates encode the operational
  knowledge an AI needs (the ui_dump → tap → verify loop, bounds-center math,
  safety rules for a real user device). This is the cheapest form of
  "agent skill": pure text, no new protocol surface.

### Known limitations of the shipped pack

- Text-only `type_text` (printable ASCII); Unicode needs `am broadcast` +
  ADBKeyboard or a protocol-level stdin frame.
- `screenshot`/`ui_dump` cost several round trips (capture + N chunks);
  fine for automation loops, not for screen streaming.
- Chunked transfer assumes the file doesn't change mid-transfer (detected via
  size mismatch, not prevented).
- No streaming/progress for long transfers — MCP result arrives at the end.

## Roadmap (not yet built)

Ordered by value per unit of effort:

1. **MCP resources** (`resources/list`, `resources/read`) — expose the
   connected-device registry and last screenshot as subscribable resources;
   clients like Claude Desktop render them natively. Server-only change.
2. **Unicode text input** — pair `type_text` with an optional on-device
   broadcast approach (requires a tiny agent addition or a helper APK), or
   document the ADBKeyboard route in the prompt skill.
3. **Binary-safe protocol v2 frame** — a `file` frame type with base64 body
   and an explicit transfer id, replacing multi-chunk exec for pulls/pushes.
   Needs agent + relay changes (versioned handshake already exists via `vc`).
4. **Multi-device broadcast** — `run_shell_all` / fleet fan-out with
   per-device results; registry already supports N devices, tool layer needs
   a concurrency-limited map-reduce.
5. **FCM hybrid wake** (blocked, see `DESIGN.md` §7) — requires a Firebase
   project; the agent-side seam is already marked in `ConnectionManager.kt`.
6. **Screen streaming** — repeated `screencap` is too slow; would need
   on-device `screenrecord` piping or MediaProjection in the agent (a real
   app change with a foreground-service permission).
7. **Prompt-pack extensibility** — load skill templates from a directory so
   users can add their own without recompiling (keep the built-ins as the
   default set).
8. **Rate limiting per tool** — the relay limits WS connections per IP, but
   MCP tool calls are only bounded by `maxPendingPerDevice`; a token bucket
   per AI token would contain runaway automation loops.

## Testing notes

- `internal/tools` tests use a rule-based fake executor; validation tests
  assert both the error result and zero device calls.
- Quote-escaping tests pin exact command strings (`input text 'a'\''b;$(id)'`)
  so a regression in `shellQuote` fails loudly.
- Round-trip coverage for chunked transfer lives in `files_test.go` /
  `screen_test.go` (multi-chunk reads, size mismatch, truncation).
