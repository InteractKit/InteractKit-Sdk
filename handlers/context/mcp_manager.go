package context

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"

	"interactkit/core"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpConnection holds the state for a single MCP server connection.
type mcpConnection struct {
	name       string
	toolPrefix string
	session    *mcp.ClientSession
	tools      map[string]*mcp.Tool // keyed by interactkit tool ID
}

// MCPManager manages connections to one or more MCP servers.
type MCPManager struct {
	client      *mcp.Client
	connections []*mcpConnection
	logger      *core.Logger
	mu          sync.RWMutex
}

// NewMCPManager creates an MCPManager. Call Connect to establish sessions.
func NewMCPManager(logger *core.Logger) *MCPManager {
	return &MCPManager{
		client: mcp.NewClient(&mcp.Implementation{
			Name:    "interactkit",
			Version: "1.0.0",
		}, nil),
		logger: logger,
	}
}

// Connect establishes connections to all configured MCP servers and discovers their tools.
// Individual connection failures are logged and skipped (non-fatal).
func (m *MCPManager) Connect(ctx context.Context, configs []core.MCPServerConfig) error {
	for _, cfg := range configs {
		conn, err := m.connectOne(ctx, cfg)
		if err != nil {
			m.logger.With(map[string]any{
				"server": cfg.Name,
				"error":  err.Error(),
			}).Error("failed to connect to MCP server, skipping")
			continue
		}
		m.connections = append(m.connections, conn)
		m.logger.With(map[string]any{
			"server":    cfg.Name,
			"num_tools": len(conn.tools),
		}).Info("connected to MCP server")
	}
	return nil
}

func (m *MCPManager) connectOne(ctx context.Context, cfg core.MCPServerConfig) (*mcpConnection, error) {
	transport, err := buildTransport(cfg)
	if err != nil {
		return nil, fmt.Errorf("build transport: %w", err)
	}

	session, err := m.client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}

	conn := &mcpConnection{
		name:       cfg.Name,
		toolPrefix: cfg.ToolPrefix,
		session:    session,
		tools:      make(map[string]*mcp.Tool),
	}

	// Discover tools.
	toolsResult, err := session.ListTools(ctx, nil)
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("list tools: %w", err)
	}
	for _, tool := range toolsResult.Tools {
		toolID := makeToolID(cfg.ToolPrefix, tool.Name)
		conn.tools[toolID] = tool
	}

	return conn, nil
}

func buildTransport(cfg core.MCPServerConfig) (mcp.Transport, error) {
	switch {
	case cfg.Command != nil:
		cmd := exec.Command(cfg.Command.Path, cfg.Command.Args...)
		if len(cfg.Command.Env) > 0 {
			cmd.Env = append(cmd.Environ(), cfg.Command.Env...)
		}
		return &mcp.CommandTransport{Command: cmd}, nil
	case cfg.SSE != nil:
		return &mcp.SSEClientTransport{Endpoint: cfg.SSE.URL}, nil
	case cfg.Streamable != nil:
		return &mcp.StreamableClientTransport{Endpoint: cfg.Streamable.URL}, nil
	default:
		return nil, fmt.Errorf("no transport configured for MCP server %q", cfg.Name)
	}
}

func makeToolID(prefix, toolName string) string {
	if prefix == "" {
		return toolName
	}
	return prefix + "_" + toolName
}

// Tools returns all discovered MCP tools converted to core.LLMTool format.
func (m *MCPManager) Tools() []core.LLMTool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var tools []core.LLMTool
	for _, conn := range m.connections {
		for toolID, mcpTool := range conn.tools {
			tools = append(tools, convertMCPTool(toolID, mcpTool))
		}
	}
	return tools
}

// CallTool forwards a tool call to the appropriate MCP server and returns the result as a string.
func (m *MCPManager) CallTool(ctx context.Context, toolID string, params map[string]any) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, conn := range m.connections {
		mcpTool, ok := conn.tools[toolID]
		if !ok {
			continue
		}

		result, err := conn.session.CallTool(ctx, &mcp.CallToolParams{
			Name:      mcpTool.Name,
			Arguments: params,
		})
		if err != nil {
			return "", fmt.Errorf("MCP call to %s/%s failed: %w", conn.name, mcpTool.Name, err)
		}

		return extractMCPResult(result), nil
	}

	return "", fmt.Errorf("no MCP server owns tool %q", toolID)
}

// extractMCPResult converts MCP CallToolResult content into a single string for the LLM.
func extractMCPResult(result *mcp.CallToolResult) string {
	var parts []string
	for _, c := range result.Content {
		switch content := c.(type) {
		case *mcp.TextContent:
			parts = append(parts, content.Text)
		case *mcp.ImageContent:
			parts = append(parts, fmt.Sprintf("[Image: %s]", content.MIMEType))
		case *mcp.AudioContent:
			parts = append(parts, fmt.Sprintf("[Audio: %s]", content.MIMEType))
		default:
			parts = append(parts, "[unsupported content type]")
		}
	}

	text := strings.Join(parts, "\n")
	if result.IsError {
		return "Error: " + text
	}
	return text
}

// Close tears down all MCP sessions.
func (m *MCPManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, conn := range m.connections {
		if err := conn.session.Close(); err != nil {
			m.logger.With(map[string]any{
				"server": conn.name,
				"error":  err.Error(),
			}).Error("error closing MCP session")
		}
	}
	m.connections = nil
}
