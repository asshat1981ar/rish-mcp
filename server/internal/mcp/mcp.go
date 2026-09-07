// Package mcp is a minimal, hand-rolled MCP (Model Context Protocol) server:
// just enough JSON-RPC 2.0 over stateless HTTP to serve a fixed set of tools.
// No official Go SDK exists yet (see docs/DESIGN.md's accepted risk on this),
// so only the methods rish-mcp actually needs are implemented.
package mcp

import (
	"context"
	"encoding/json"
	"strings"
)

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Property is one JSON-Schema property of a tool's input.
type Property struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

// InputSchema is the (deliberately small) subset of JSON Schema rish-mcp's
// tools need to describe their arguments.
type InputSchema struct {
	Type       string              `json:"type"`
	Properties map[string]Property `json:"properties"`
	Required   []string            `json:"required,omitempty"`
}

type ContentItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	// Data and MimeType carry non-text MCP content (e.g. base64 PNG images
	// returned by the screenshot tool). Text items leave both empty.
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// Prompt is one MCP prompt template ("skill"): a reusable instruction set the
// AI client can fetch via prompts/get. Arguments are interpolated by simple
// {{name}} replacement at fetch time.
type Prompt struct {
	Name        string
	Description string
	Arguments   []PromptArgument
	Text        string
}

type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

type promptDef struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

// CallResult is a tool's response, shaped like MCP's tools/call result.
type CallResult struct {
	Content []ContentItem `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// Tool is one MCP tool: its schema plus the handler that runs it.
type Tool struct {
	Name        string
	Description string
	InputSchema InputSchema
	Handler     func(ctx context.Context, args json.RawMessage) (CallResult, error)
}

type toolDef struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema InputSchema `json:"inputSchema"`
}

// Server dispatches JSON-RPC requests to a fixed, statically registered set
// of tools. It is stateless: one Server can serve every /mcp request.
type Server struct {
	Name    string
	Version string
	tools   []Tool
	prompts []Prompt
}

func NewServer(name, version string) *Server {
	return &Server{Name: name, Version: version}
}

func (s *Server) RegisterTool(t Tool) {
	s.tools = append(s.tools, t)
}

// RegisterPrompt adds a prompt template ("skill") served via prompts/list and
// prompts/get.
func (s *Server) RegisterPrompt(p Prompt) {
	s.prompts = append(s.prompts, p)
}

// Handle processes one JSON-RPC request and returns the response to send,
// or nil when req was a notification (no response expected/allowed).
func (s *Server) Handle(ctx context.Context, req Request) *Response {
	switch req.Method {
	case "initialize":
		caps := map[string]any{"tools": map[string]any{}}
		if len(s.prompts) > 0 {
			caps["prompts"] = map[string]any{"listChanged": false}
		}
		return &Response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    caps,
			"serverInfo":      map[string]any{"name": s.Name, "version": s.Version},
		}}
	case "notifications/initialized", "notifications/cancelled":
		return nil
	case "tools/list":
		defs := make([]toolDef, 0, len(s.tools))
		for _, t := range s.tools {
			defs = append(defs, toolDef{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
		}
		return &Response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": defs}}
	case "prompts/list":
		defs := make([]promptDef, 0, len(s.prompts))
		for _, p := range s.prompts {
			defs = append(defs, promptDef{Name: p.Name, Description: p.Description, Arguments: p.Arguments})
		}
		return &Response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"prompts": defs}}
	case "prompts/get":
		return s.handlePromptGet(req)
	case "tools/call":
		return s.handleToolsCall(ctx, req)
	default:
		return &Response{JSONRPC: "2.0", ID: req.ID, Error: &RPCError{Code: -32601, Message: "method not found"}}
	}
}

type promptGetParams struct {
	Name      string            `json:"name"`
	Arguments map[string]string `json:"arguments"`
}

// handlePromptGet resolves a registered prompt template, interpolating any
// {{name}} placeholders from the supplied arguments.
func (s *Server) handlePromptGet(req Request) *Response {
	var p promptGetParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return &Response{JSONRPC: "2.0", ID: req.ID, Error: &RPCError{Code: -32602, Message: "invalid params"}}
	}
	for _, prompt := range s.prompts {
		if prompt.Name != p.Name {
			continue
		}
		text := prompt.Text
		for k, v := range p.Arguments {
			text = strings.ReplaceAll(text, "{{"+k+"}}", v)
		}
		return &Response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"description": prompt.Description,
			"messages": []map[string]any{{
				"role":    "user",
				"content": map[string]string{"type": "text", "text": text},
			}},
		}}
	}
	return &Response{JSONRPC: "2.0", ID: req.ID, Error: &RPCError{Code: -32602, Message: "unknown prompt: " + p.Name}}
}

type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (s *Server) handleToolsCall(ctx context.Context, req Request) *Response {
	var p callParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return &Response{JSONRPC: "2.0", ID: req.ID, Error: &RPCError{Code: -32602, Message: "invalid params"}}
	}
	for _, t := range s.tools {
		if t.Name != p.Name {
			continue
		}
		res, err := t.Handler(ctx, p.Arguments)
		if err != nil {
			return &Response{JSONRPC: "2.0", ID: req.ID, Result: CallResult{
				Content: []ContentItem{{Type: "text", Text: "error: " + err.Error()}},
				IsError: true,
			}}
		}
		return &Response{JSONRPC: "2.0", ID: req.ID, Result: res}
	}
	return &Response{JSONRPC: "2.0", ID: req.ID, Error: &RPCError{Code: -32602, Message: "unknown tool: " + p.Name}}
}
