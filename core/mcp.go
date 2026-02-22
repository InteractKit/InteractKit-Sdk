package core

// MCPServerConfig describes a single MCP server to connect to.
type MCPServerConfig struct {
	// Name is a human-readable label for this server (used in logs).
	Name string `json:"name"`

	// ToolPrefix is an optional string prepended to all tool IDs from this server
	// to avoid collisions between servers. E.g. prefix "weather" turns tool "forecast"
	// into "weather_forecast". When empty, tool names are used as-is.
	ToolPrefix string `json:"tool_prefix,omitempty"`

	// Exactly one transport field should be set:

	// Command configures a stdio-based MCP server launched as a subprocess.
	Command *MCPCommandConfig `json:"command,omitempty"`

	// SSE configures a server-sent-events MCP server at a URL.
	SSE *MCPURLConfig `json:"sse,omitempty"`

	// Streamable configures a streamable HTTP MCP server at a URL.
	Streamable *MCPURLConfig `json:"streamable,omitempty"`
}

// MCPCommandConfig describes a command-based (stdio) MCP server.
type MCPCommandConfig struct {
	// Path is the executable path.
	Path string `json:"path"`
	// Args are command-line arguments.
	Args []string `json:"args,omitempty"`
	// Env are additional environment variables (KEY=VALUE).
	Env []string `json:"env,omitempty"`
}

// MCPURLConfig describes an HTTP-based MCP server.
type MCPURLConfig struct {
	// URL is the endpoint.
	URL string `json:"url"`
	// Headers are optional HTTP headers to include.
	Headers map[string]string `json:"headers,omitempty"`
}
