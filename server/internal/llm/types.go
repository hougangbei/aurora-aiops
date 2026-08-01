package llm

// APIStyle selects the OpenAI-compatible request/response shape.
type APIStyle string

const (
	StyleChatCompletions APIStyle = "chat_completions"
	StyleResponses       APIStyle = "responses"
)

// Message is one chat turn for the chat_completions style.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Request describes one model generation call. Model defaults to the client's
// configured model when empty. Exactly one of Messages (chat_completions) or
// Input (responses) should be populated.
type Request struct {
	Model    string    `json:"model,omitempty"`
	Messages []Message `json:"messages,omitempty"`
	Input    string    `json:"input,omitempty"`
	JSONMode bool      `json:"-"`
}

// Usage is the normalized token accounting across both API styles.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Response is the normalized model output.
type Response struct {
	Text  string
	Model string
	Usage Usage
}
