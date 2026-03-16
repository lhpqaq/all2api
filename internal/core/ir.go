package core

type Endpoint string

const (
	EndpointAnthropicMessages Endpoint = "anthropic.messages"
	EndpointOpenAIChat        Endpoint = "openai.chat.completions"
	EndpointOpenAIResponses   Endpoint = "openai.responses"
)

type StreamEvent struct {
	TextDelta     string
	ThinkingDelta string
	Error         error
	Done          bool
}

type CoreRequest struct {
	Endpoint      Endpoint
	Upstream      string
	Model         string
	Thinking      bool
	Stream        bool
	StreamChannel chan StreamEvent

	System   string
	Messages []Message

	Tools      []ToolDef
	ToolChoice ToolChoice

	MaxTokens   int
	Temperature *float64
}

type Message struct {
	Role       string
	Content    string
	ToolCallID string
	ToolCalls  []ToolCall
}

type ToolDef struct {
	Name        string
	Description string
	InputSchema map[string]any
}

type ToolChoice struct {
	Mode string
	Name string
}

type ToolCall struct {
	ID   string
	Name string
	Args map[string]any
}

type CoreResult struct {
	Text      string
	Thinking  string
	ToolCalls []ToolCall
}
