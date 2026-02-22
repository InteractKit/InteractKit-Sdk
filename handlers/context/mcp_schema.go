package context

import (
	"fmt"
	"strings"

	"interactkit/core"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// convertMCPTool converts an MCP Tool to an InteractKit LLMTool.
func convertMCPTool(toolID string, tool *mcp.Tool) core.LLMTool {
	var params []core.Parameter

	// InputSchema arrives as map[string]any from the client side.
	schema, ok := tool.InputSchema.(map[string]any)
	if ok {
		params = extractParameters(schema)
	}

	return core.LLMTool{
		Name:        toolID,
		ToolId:      toolID,
		Description: tool.Description,
		Parameters:  params,
	}
}

// extractParameters pulls top-level properties from a JSON Schema map and
// converts them into a flat []core.Parameter slice.
func extractParameters(schema map[string]any) []core.Parameter {
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		return nil
	}

	requiredSet := make(map[string]bool)
	if reqList, ok := schema["required"].([]any); ok {
		for _, r := range reqList {
			if s, ok := r.(string); ok {
				requiredSet[s] = true
			}
		}
	}

	var params []core.Parameter
	for name, raw := range props {
		propSchema, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		params = append(params, convertProperty(name, propSchema, requiredSet[name]))
	}
	return params
}

// convertProperty converts a single JSON Schema property map to a core.Parameter.
func convertProperty(name string, schema map[string]any, required bool) core.Parameter {
	param := core.Parameter{
		Name:     name,
		Required: required,
		Type:     mapSchemaType(schema),
	}

	if desc, ok := schema["description"].(string); ok {
		param.Description = desc
	}

	// Use first example if available.
	if examples, ok := schema["examples"].([]any); ok && len(examples) > 0 {
		param.Example = fmt.Sprintf("%v", examples[0])
	}

	// For enum types, append the allowed values to the description.
	if enumList, ok := schema["enum"].([]any); ok && len(enumList) > 0 {
		vals := make([]string, len(enumList))
		for i, v := range enumList {
			vals[i] = fmt.Sprintf("%v", v)
		}
		if param.Description != "" {
			param.Description += " "
		}
		param.Description += "(one of: " + strings.Join(vals, ", ") + ")"
	}

	return param
}

// mapSchemaType maps a JSON Schema type string to a core.LLMParameterType.
func mapSchemaType(schema map[string]any) core.LLMParamterType {
	t, _ := schema["type"].(string)
	switch t {
	case "string":
		return core.LLMParameterTypeString
	case "integer", "number":
		return core.LLMParameterTypeInteger
	case "boolean":
		return core.LLMParameterTypeBoolean
	case "object":
		return core.LLMParameterTypeObject
	default:
		return core.LLMParameterTypeString
	}
}
