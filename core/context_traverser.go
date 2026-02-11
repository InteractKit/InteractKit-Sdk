package core

// =========================
// Top-Level Agent Definition
// =========================

type AgentDefinition struct {
	AgentTaskSummary string        `json:"AgentTaskSummary"`
	Variables        []VariableDef `json:"Variables,omitempty"`
	Constants        []ConstantDef `json:"Constants,omitempty"`
	Profiles         []ProfileDef  `json:"Profiles,omitempty"`
	Tasks            []TaskDef     `json:"Tasks,omitempty"`
	InitialTaskID    string        `json:"InitialTaskID"`
	InitialProfiles  []string      `json:"InitalProfiles,omitempty"`
}

// =========
// Variables
// =========

type VariableDef struct {
	Id          string       `json:"Id"`
	Description string       `json:"Description,omitempty"`
	Type        VariableType `json:"Type"`
}

type VariableType struct {
	Type         string               `json:"Type"` // string, number, boolean, date, array, object, currentTime
	Enum         []string             `json:"Enum,omitempty"`
	RegexChecks  []RegexCheck         `json:"RegexChecks,omitempty"`
	Example      interface{}          `json:"Example,omitempty"`
	Persistence  *VariablePersistence `json:"Persistance,omitempty"`
	InitialValue interface{}          `json:"InitialValue,omitempty"`

	// Array-specific
	OfValue *VariableType `json:"OfValue,omitempty"`

	// Object-specific
	Properties map[string]VariableType `json:"Properties,omitempty"`
}

type RegexCheck struct {
	Regex string `json:"Regex"`
	Error string `json:"Error"`
}

type VariablePersistence struct {
	Mode      string `json:"Mode"`      // session | persistent
	WriteMode string `json:"WriteMode"` // overwrite | append | increment
}

// =========
// Constants
// =========

type ConstantDef struct {
	Id    string      `json:"Id"`
	Value interface{} `json:"Value"`
}

// ========
// Profiles
// ========

type ProfileDef struct {
	Id                string                 `json:"Id"`
	Type              string                 `json:"Type"` // TTS | STT | LLM | VAD | VectorDB | VideoAI
	Provider          string                 `json:"Provider"`
	Config            map[string]interface{} `json:"Config,omitempty"`
	FallbackProfileId string                 `json:"FallbackProfileId,omitempty"`
}

// =====
// Tasks
// =====

type TaskDef struct {
	Namespace        string        `json:"Namespace,omitempty"`
	Id               string        `json:"Id"`
	Instructions     []Instruction `json:"Instructions"`
	Searchable       bool          `json:"Searchable,omitempty"`
	SearchTag        string        `json:"SearchTag,omitempty"`
	RoutingRules     []RoutingRule `json:"RoutingRules,omitempty"`
	PreInstructions  []Instruction `json:"PreInstructions,omitempty"`
	PostInstructions []Instruction `json:"PostInstructions,omitempty"`
	ToolIds          []string      `json:"ToolIds,omitempty"`
}

// ==============
// Instructions
// ==============

type Instruction struct {
	WaitFor       *WaitForInstruction       `json:"waitFor,omitempty"`
	WaitForMS     *int                      `json:"waitForMS,omitempty"`
	AskUser       *string                   `json:"askUser,omitempty"`
	SetVariables  map[string]interface{}    `json:"SetVariables,omitempty"`
	Parse         *ParseInstruction         `json:"Parse,omitempty"`
	GetVariable   *GetVariableInstruction   `json:"GetVariable,omitempty"`
	Loop          *LoopInstruction          `json:"Loop,omitempty"`
	TestSplit     *TestSplitInstruction     `json:"TestSplit,omitempty"`
	CallTool      *CallToolInstruction      `json:"CallTool,omitempty"`
	UpdateProfile *UpdateProfileInstruction `json:"UpdateProfile,omitempty"`
}

// --- Instruction Subtypes ---

type WaitForInstruction struct {
	Event     string `json:"waitFor"`
	TimeoutMS int    `json:"TimeoutMS,omitempty"`
}

type ParseInstruction struct {
	Description string `json:"Parse"`
	Target      string `json:"Target"`
}

type GetVariableInstruction struct {
	VariableId           string        `json:"GetVariable"`
	FallbackInstructions []Instruction `json:"FallbackInstructions,omitempty"`
}

type LoopInstruction struct {
	Over          string        `json:"Over"`
	ItemVariable  string        `json:"ItemVariable"`
	Instructions  []Instruction `json:"Instructions"`
	MaxIterations int           `json:"MaxIterations,omitempty"`
}

type TestSplitInstruction struct {
	Percentage    float64       `json:"Percentage"`
	RandomSeed    int           `json:"RandomSeed,omitempty"`
	InstructionsA []Instruction `json:"InstructionsA"`
	InstructionsB []Instruction `json:"InstructionsB"`
}

type CallToolInstruction struct {
	FunctionName         string                 `json:"CallTool"`
	Params               map[string]interface{} `json:"Params,omitempty"`
	OutputVariable       string                 `json:"OutputVariable,omitempty"`
	WaitForCompletion    *bool                  `json:"WaitForCompletion,omitempty"`
	FallbackInstructions []Instruction          `json:"FallbackInstructions,omitempty"`
}

type UpdateProfileInstruction struct {
	ProfileId string                 `json:"ProfileId"`
	Config    map[string]interface{} `json:"Config"`
}

// ==============
// Routing Rules
// ==============

type RoutingRule struct {
	Condition       *Condition `json:"Condition,omitempty"`
	TargetTaskId    string     `json:"TargetTaskId"`
	AppendToSummary string     `json:"AppendToSummary,omitempty"`
}

type Condition struct {
	Value1    interface{} `json:"Value1,omitempty"`
	Operation string      `json:"Operation,omitempty"` // == != > < array-in array-not-in
	Value2    interface{} `json:"Value2,omitempty"`
	And       []Condition `json:"And,omitempty"`
	Or        []Condition `json:"Or,omitempty"`
}
