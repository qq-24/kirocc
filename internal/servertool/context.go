package servertool

import (
	"github.com/d-kuro/kirocc/internal/anthropic"
)

// Well-known Claude Code tool names that the proxy intercepts and executes
// locally instead of letting the client handle them.
const (
	ToolNameWebSearch = "WebSearch"
	ToolNameWebFetch  = "WebFetch"
)

// Context holds server tool definitions extracted from the request.
// WebSearch/WebFetch are intercepted by the proxy instead of being
// executed by Claude Code client (which requires Anthropic's own API).
type Context struct {
	ServerTools map[string]anthropic.Tool // name → tool definition
}

// interceptedName reports whether the tool is one we want to intercept.
func interceptedName(name string) bool {
	return name == ToolNameWebSearch || name == ToolNameWebFetch
}

// NewContext scans tools for Claude Code tools we intercept (WebSearch, WebFetch).
// Returns nil if no such tools are found.
func NewContext(tools []anthropic.Tool) *Context {
	var ctx *Context
	for _, t := range tools {
		if !interceptedName(t.Name) && !t.IsServerTool() {
			continue
		}
		if ctx == nil {
			ctx = &Context{ServerTools: make(map[string]anthropic.Tool)}
		}
		ctx.ServerTools[t.Name] = t
	}
	return ctx
}

// IsServerTool reports whether the given tool name should be intercepted.
func (c *Context) IsServerTool(name string) bool {
	if c == nil {
		return false
	}
	_, ok := c.ServerTools[name]
	return ok
}

// IsWebSearch reports whether the tool name is WebSearch (or a web_search_* type).
func IsWebSearch(t anthropic.Tool) bool {
	return t.Name == ToolNameWebSearch || t.IsWebSearchTool()
}

// IsWebFetch reports whether the tool name is WebFetch (or a web_fetch_* type).
func IsWebFetch(t anthropic.Tool) bool {
	return t.Name == ToolNameWebFetch || t.IsWebFetchTool()
}

