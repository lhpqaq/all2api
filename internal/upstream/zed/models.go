package zed

type zedProviderRequest struct {
	Model       string                 `json:"model"`
	MaxTokens   int                    `json:"max_tokens"`
	System      string                 `json:"system,omitempty"`
	Messages    []zedMessage           `json:"messages"`
	Temperature *float64               `json:"temperature,omitempty"`
	Thinking    map[string]interface{} `json:"thinking,omitempty"`
	Tools       []any                  `json:"tools,omitempty"`
	ToolChoice  any                    `json:"tool_choice,omitempty"`
	Stream      bool                   `json:"stream,omitempty"`
}

type zedMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type zedPayload struct {
	ThreadID        string             `json:"thread_id"`
	PromptID        string             `json:"prompt_id"`
	Intent          string             `json:"intent"`
	Provider        string             `json:"provider"`
	Model           string             `json:"model"`
	ProviderRequest zedProviderRequest `json:"provider_request"`
}
