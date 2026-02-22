// Command typegen parses Go struct definitions and generates TypeScript
// interfaces consumed by the UI. Run from the project root:
//
//	go run ./cmd/typegen -out ui/src/types/generated.ts
//
// The generated file replaces the hand-maintained settings.ts so that Go
// struct changes automatically propagate to the frontend.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

// structInfo stores parsed information about a Go struct.
type structInfo struct {
	name   string
	fields []fieldInfo
	pkg    string // source package path (for dedup)
}

// fieldInfo stores parsed information about a struct field.
type fieldInfo struct {
	jsonName  string
	goType    string
	optional  bool
	tsType    string // resolved TS type
	isPointer bool
}

// typeMapping maps Go type strings to TypeScript type strings.
var typeMapping = map[string]string{
	"string":                 "string",
	"int":                    "number",
	"int8":                   "number",
	"int16":                  "number",
	"int32":                  "number",
	"int64":                  "number",
	"uint":                   "number",
	"uint8":                  "number",
	"uint16":                 "number",
	"uint32":                 "number",
	"uint64":                 "number",
	"float32":               "number",
	"float64":               "number",
	"bool":                   "boolean",
	"any":                    "unknown",
	"interface{}":            "unknown",
	"json.RawMessage":        "unknown",
	"map[string]string":      "Record<string, string>",
	"map[string]interface{}": "Record<string, unknown>",
	"map[string]any":         "Record<string, unknown>",
}

// typeAliases maps Go named types (e.g. LLMMessageRole) to their underlying
// Go primitive. Populated at parse time by scanning `type X <primitive>` decls.
var typeAliases = map[string]string{}

// constValues maps a Go named type to its declared const string values.
// e.g. "LLMMessageRole" -> ["user", "assistant", "system", "tool"]
// Populated at parse time by scanning const blocks.
var constValues = map[string][]string{}

// secretStructs are struct names where the url field IS a secret (transport provider).
// For other structs like SessionAPIConfig, "url" is user-facing.
var secretStructURLOwners = map[string]bool{
	"LiveKitProviderConfig": true,
}

// requiredFields lists struct+field combos that must stay required (not optional)
// in the generated TS even though we default everything to optional. These are
// identity/key fields that are always present at runtime.
var requiredFields = map[string]map[string]bool{
	"SessionInfo":    {"session_id": true, "started_at": true, "status": true, "log_file_size": true, "last_modified": true},
	"LogEntry":       {"ts": true, "level": true, "msg": true},
	"LLMMessage":     {"role": true, "message": true},
	"ContextMessage": {"role": true, "message": true},
	"TaskDef":        {"id": true},
	"TaskVariable":   {"name": true},
}

// structsToGenerate lists the Go struct names to include in generation,
// in the order they should appear in the output.
var structsToGenerate = []string{
	// Top-level settings
	"SettingsConfig",
	"SessionAPIConfig",
	"TransportFactoryConfig",
	"LiveKitProviderConfig",
	"DailyProviderConfig",
	// Session config
	"SessionConfig",
	"SessionSTTConfig",
	"SessionLLMConfig",
	"SessionTTSConfig",
	"SessionContextConfig",
	// MCP config
	"MCPServerConfig",
	"MCPCommandConfig",
	"MCPURLConfig",
	// Handler configs
	"STTConfig",
	"LLMHandlerConfig",
	"TTSConfig",
	"LLMContextManagerConfig",
	"ContextConfig",
	// Core LLM types
	"LLMContext",
	"LLMMessage",
	"LLMTool",
	"Parameter",
	// Task group
	"TaskGroupConfig",
	"TaskDef",
	"TaskVariable",
	// Service configs
	"DeepgramConfig",
	"services/openai/llm:Config", // OpenAI LLM Config (disambiguated)
	"DepgramTTSConfig",
	"ElevenLabsTTSConfig",
	"CartesiaTTSConfig",
	// STT factory
	"STTFactoryConfig",
	// LLM factory
	"LLMFactoryConfig",
	// TTS factory
	"TTSFactoryConfig",
	// API response types
	"SessionInfo",
	"LogEntry",
}

// tsRenames maps Go struct names to preferred TypeScript interface names.
var tsRenames = map[string]string{
	"SettingsConfig":          "Settings",
	"TransportFactoryConfig":  "TransportConfig",
	"LiveKitProviderConfig":   "LiveKitConfig",
	"DailyProviderConfig":     "DailyConfig",
	"SessionSTTConfig":        "SttConfig",
	"SessionLLMConfig":        "LlmConfig",
	"SessionTTSConfig":        "TtsConfig",
	"SessionContextConfig":    "ContextConfig",
	"STTConfig":               "SttHandlerConfig",
	"LLMHandlerConfig":        "LlmHandlerConfig",
	"TTSConfig":               "TtsHandlerConfig",
	"LLMContextManagerConfig": "ContextManagerConfig",
	"ContextConfig":           "ContextHandlerConfig",
	"LLMContext":              "LlmContext",
	"LLMMessage":              "ContextMessage",
	"LLMTool":                 "ContextTool",
	"Parameter":               "ToolParameter",
	"TaskDef":                 "TaskDefinition",
	"DeepgramConfig":          "DeepgramSttConfig",
	"services/openai/llm:Config": "OpenAiLlmConfig",
	"DepgramTTSConfig":        "DeepgramTtsConfig",
	"STTFactoryConfig":        "SttServiceConfig",
	"LLMFactoryConfig":        "LlmServiceConfig",
	"TTSFactoryConfig":        "TtsServiceConfig",
	"ElevenLabsTTSConfig":     "ElevenLabsTtsConfig",
	"CartesiaTTSConfig":       "CartesiaTtsConfig",
	"SessionAPIConfig":        "SessionApiConfig",
	"MCPServerConfig":         "McpServerConfig",
	"MCPCommandConfig":        "McpCommandConfig",
	"MCPURLConfig":            "McpUrlConfig",
}

// goTypeToTSRef maps a Go type reference (struct name) to its TS name.
var goTypeToTSRef = map[string]string{}

func init() {
	// Build reverse mapping: every struct we generate gets a TS name.
	// For qualified keys like "services/openai/llm:Config", also register
	// the plain struct name so field type resolution can find it.
	for _, name := range structsToGenerate {
		tsName := name
		if rename, ok := tsRenames[name]; ok {
			tsName = rename
		}
		goTypeToTSRef[name] = tsName
		// Also register the plain struct name (part after ":") for type resolution.
		if idx := strings.LastIndex(name, ":"); idx >= 0 {
			plain := name[idx+1:]
			goTypeToTSRef[plain] = tsName
		}
	}
}

func main() {
	outPath := flag.String("out", "ui/src/types/generated.ts", "output TypeScript file path")
	flag.Parse()

	root, err := os.Getwd()
	if err != nil {
		fatal("getwd: %v", err)
	}

	// Auto-discover all directories containing .go files.
	dirs, err := discoverGoDirs(root)
	if err != nil {
		fatal("discover dirs: %v", err)
	}

	// Parse all structs from all discovered directories.
	// Store under both "StructName" and "rel/dir:StructName" keys.
	// The qualified key allows disambiguation when multiple packages
	// define a struct with the same name (e.g. "Config").
	allStructs := map[string]*structInfo{}
	for _, dir := range dirs {
		structs, err := parseDir(dir)
		if err != nil {
			// Skip directories that fail to parse (e.g. generated code).
			fmt.Fprintf(os.Stderr, "warning: skipping %s: %v\n", dir, err)
			continue
		}
		relDir, _ := filepath.Rel(root, dir)
		for name, si := range structs {
			qualifiedKey := relDir + ":" + name
			allStructs[qualifiedKey] = si
			// Only store under plain name if not already claimed (first wins).
			if _, exists := allStructs[name]; !exists {
				allStructs[name] = si
			}
		}
	}

	// Generate TypeScript.
	var buf bytes.Buffer
	buf.WriteString("// Code generated by cmd/typegen; DO NOT EDIT.\n")
	buf.WriteString("// Source: Go structs from core/, handlers/, factories/, services/\n")
	buf.WriteString("//\n")
	buf.WriteString("// Regenerate: go run ./cmd/typegen -out ui/src/types/generated.ts\n\n")

	for _, goName := range structsToGenerate {
		si, ok := allStructs[goName]
		if !ok {
			fmt.Fprintf(os.Stderr, "warning: struct %q not found, skipping\n", goName)
			continue
		}
		tsName := goName
		if rename, ok := tsRenames[goName]; ok {
			tsName = rename
		}
		writeInterface(&buf, tsName, si, goName)
	}

	// Write the API types that don't come from struct parsing.
	writeManualAPITypes(&buf)

	absOut := *outPath
	if !filepath.IsAbs(absOut) {
		absOut = filepath.Join(root, absOut)
	}
	if err := os.MkdirAll(filepath.Dir(absOut), 0o755); err != nil {
		fatal("mkdir: %v", err)
	}
	if err := os.WriteFile(absOut, buf.Bytes(), 0o644); err != nil {
		fatal("write: %v", err)
	}
	fmt.Fprintf(os.Stderr, "wrote %s (%d bytes)\n", absOut, buf.Len())
}

// discoverGoDirs walks the project tree and returns all directories containing
// .go files, skipping vendor, .git, node_modules, and the typegen cmd itself.
func discoverGoDirs(root string) ([]string, error) {
	skipDirs := map[string]bool{
		"vendor":       true,
		"node_modules": true,
		".git":         true,
		".next":        true,
		"typegen":      true, // skip ourselves
	}

	seen := map[string]bool{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(info.Name(), ".go") && !strings.HasSuffix(info.Name(), "_test.go") {
			dir := filepath.Dir(path)
			seen[dir] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	dirs := make([]string, 0, len(seen))
	for d := range seen {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	return dirs, nil
}

// parseDir parses all .go files in a directory and extracts struct definitions.
func parseDir(dir string) (map[string]*structInfo, error) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	result := map[string]*structInfo{}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				genDecl, ok := decl.(*ast.GenDecl)
				if !ok {
					continue
				}

				switch genDecl.Tok {
				case token.TYPE:
					for _, spec := range genDecl.Specs {
						ts, ok := spec.(*ast.TypeSpec)
						if !ok {
							continue
						}
						// Collect type aliases (e.g. `type LLMMessageRole string`).
						if ident, ok := ts.Type.(*ast.Ident); ok {
							typeAliases[ts.Name.Name] = ident.Name
							continue
						}
						st, ok := ts.Type.(*ast.StructType)
						if !ok {
							continue
						}
						si := parseStruct(ts.Name.Name, st, dir)
						if si != nil {
							result[ts.Name.Name] = si
						}
					}

				case token.CONST:
					// Collect const values grouped by their named type.
					// e.g. `const LLMMessageRoleUser LLMMessageRole = "user"`
					for _, spec := range genDecl.Specs {
						vs, ok := spec.(*ast.ValueSpec)
						if !ok || vs.Type == nil || len(vs.Values) == 0 {
							continue
						}
						typeName := typeExprToString(vs.Type)
						for _, val := range vs.Values {
							lit, ok := val.(*ast.BasicLit)
							if !ok || lit.Kind != token.STRING {
								continue
							}
							// Strip quotes from the string literal.
							s := strings.Trim(lit.Value, "\"")
							constValues[typeName] = append(constValues[typeName], s)
						}
					}
				}
			}
		}
	}
	return result, nil
}

// parseStruct extracts field info from an AST struct type.
func parseStruct(name string, st *ast.StructType, pkg string) *structInfo {
	si := &structInfo{name: name, pkg: pkg}
	for _, field := range st.Fields.List {
		if field.Tag == nil {
			continue
		}
		tag := reflect.StructTag(strings.Trim(field.Tag.Value, "`"))
		jsonTag := tag.Get("json")
		if jsonTag == "" || jsonTag == "-" {
			continue
		}

		parts := strings.Split(jsonTag, ",")
		jsonName := parts[0]
		if jsonName == "" || jsonName == "-" {
			continue
		}

		// Check if this field should be skipped (secrets).
		if shouldSkipField(name, jsonName) {
			continue
		}

		omitempty := false
		for _, p := range parts[1:] {
			if p == "omitempty" {
				omitempty = true
			}
		}

		goType := typeExprToString(field.Type)
		isPointer := isPointerType(field.Type)

		fi := fieldInfo{
			jsonName:  jsonName,
			goType:    goType,
			optional:  omitempty || isPointer,
			isPointer: isPointer,
		}
		fi.tsType = resolveType(goType)
		si.fields = append(si.fields, fi)
	}
	return si
}

// shouldSkipField returns true if a field should be excluded from TS output.
func shouldSkipField(structName, jsonName string) bool {
	if jsonName == "api_key" || jsonName == "api_secret" {
		return true
	}
	// Only skip "url" for transport provider configs (it's a secret there).
	if jsonName == "url" && secretStructURLOwners[structName] {
		return true
	}
	return false
}

// typeExprToString converts an AST type expression to a string representation.
func typeExprToString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + typeExprToString(t.X)
	case *ast.ArrayType:
		return "[]" + typeExprToString(t.Elt)
	case *ast.MapType:
		return "map[" + typeExprToString(t.Key) + "]" + typeExprToString(t.Value)
	case *ast.SelectorExpr:
		return typeExprToString(t.X) + "." + t.Sel.Name
	case *ast.InterfaceType:
		return "interface{}"
	default:
		return "unknown"
	}
}

// isPointerType checks if an AST expression is a pointer type.
func isPointerType(expr ast.Expr) bool {
	_, ok := expr.(*ast.StarExpr)
	return ok
}

// resolveType converts a Go type string to a TypeScript type string.
func resolveType(goType string) string {
	// Strip pointer prefix for lookup.
	clean := strings.TrimPrefix(goType, "*")

	// Direct mapping.
	if ts, ok := typeMapping[clean]; ok {
		return ts
	}

	// Slice types.
	if strings.HasPrefix(clean, "[]") {
		inner := resolveType(clean[2:])
		return inner + "[]"
	}

	// Map types.
	if strings.HasPrefix(clean, "map[") {
		if ts, ok := typeMapping[clean]; ok {
			return ts
		}
		return "Record<string, unknown>"
	}

	// Check if it's a known struct reference.
	if tsRef, ok := goTypeToTSRef[clean]; ok {
		return tsRef
	}

	// Qualified name (e.g., core.LLMContext).
	if idx := strings.LastIndex(clean, "."); idx >= 0 {
		shortName := clean[idx+1:]
		if tsRef, ok := goTypeToTSRef[shortName]; ok {
			return tsRef
		}
		// Check if the short name has const values -> union type.
		if vals, ok := constValues[shortName]; ok && len(vals) > 0 {
			return buildUnionLiteral(vals)
		}
		// Check if the short name is a type alias.
		if underlying, ok := typeAliases[shortName]; ok {
			return resolveType(underlying)
		}
	}

	// Check if it's a named type with known const values -> emit as union.
	if vals, ok := constValues[clean]; ok && len(vals) > 0 {
		return buildUnionLiteral(vals)
	}

	// Check if it's a type alias (e.g., LLMMessageRole -> string).
	if underlying, ok := typeAliases[clean]; ok {
		return resolveType(underlying)
	}

	// Fall back to unknown for truly unrecognized types.
	return "unknown"
}

// buildUnionLiteral returns a TS inline union type from string values.
// e.g. ["user", "assistant"] -> "'user' | 'assistant'"
func buildUnionLiteral(vals []string) string {
	quoted := make([]string, len(vals))
	for i, v := range vals {
		quoted[i] = "'" + v + "'"
	}
	return strings.Join(quoted, " | ")
}

// writeInterface writes a single TypeScript interface to the buffer.
// Fields default to optional since the Go side applies defaults via
// DefaultConfig() and JSON only contains overrides. Fields listed in
// requiredFields are emitted as required.
func writeInterface(buf *bytes.Buffer, tsName string, si *structInfo, goName string) {
	reqFields := requiredFields[goName]
	if reqFields == nil {
		reqFields = requiredFields[tsName]
	}
	fmt.Fprintf(buf, "/** Generated from Go struct: %s */\n", goName)
	fmt.Fprintf(buf, "export interface %s {\n", tsName)
	for _, f := range si.fields {
		opt := "?"
		if reqFields != nil && reqFields[f.jsonName] {
			opt = ""
		}
		fmt.Fprintf(buf, "  %s%s: %s\n", f.jsonName, opt, f.tsType)
	}
	fmt.Fprintf(buf, "}\n\n")
}

// writeManualAPITypes writes types that don't come from Go structs directly
// but are needed by the UI (API response shapes, etc.).
func writeManualAPITypes(buf *bytes.Buffer) {
	buf.WriteString("// --- API response types (not generated from Go structs) ---\n\n")
	buf.WriteString(`export interface StatusResponse {
  running: boolean
  pid: number
}

export type ApiKeys = Record<string, string>
`)
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "typegen: "+format+"\n", args...)
	os.Exit(1)
}

// Ensure consistent output by sorting map keys where needed.
var _ = sort.Strings
