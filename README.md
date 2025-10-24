# openai

A Go package for OpenAI API chat completions with support for both streaming and non-streaming responses.

## Features

- Simple, clean API for OpenAI chat completions
- Support for streaming responses
- Terminal markdown rendering for streaming responses
- Support for custom HTTP clients
- Context-aware requests
- Proper error handling

## Installation

```bash
go get github.com/jiyeol-lee/openai
```

## Usage

### Basic Non-Streaming Chat Completion

Send a prompt and wait for the full assistant reply in one shot:

See the complete example in [examples/basic/basic.go](./examples/basic/basic.go).

### Streaming Chat Completion

Receive tokens as soon as they are ready and render them manually:

See the complete example in [examples/stream/stream.go](./examples/stream/stream.go).

### Streaming Chat Completion with Markdown Output

Render the stream directly to a terminal-friendly markdown viewer while it arrives:

See the complete example in [examples/stream_markdown/stream_markdown.go](./examples/stream_markdown/stream_markdown.go).

### With Custom HTTP Client

```go
import (
    "net/http"
    "time"
)

httpClient := &http.Client{
    Timeout: 60 * time.Second,
}

client := openai.NewClient(
    os.Getenv("OPENAI_API_KEY"),
    openai.WithHTTPClient(httpClient),
)
```

## API Reference

### Types

#### `Client`

The main client for making OpenAI API requests.

#### `Message`

Represents a single message in a conversation.

- `Role`: The role of the message sender ("system", "user", or "assistant")
- `Content`: The content of the message

#### `ChatCompletionRequest`

Configuration for a chat completion request.

- `Model`: The model to use (e.g., "gpt-4", "gpt-3.5-turbo", "gpt-4-turbo")
- `Messages`: Array of messages in the conversation
- `Temperature`: Controls randomness (0.0 to 2.0), optional
- `ReasoningEffort`: Optional reasoning effort parameter ("low", "medium", "high")
- `Stream`: Set automatically by the methods (don't set manually)

#### `ChatCompletionResponse`

Response from a non-streaming completion request. Contains choices with the assistant's message.

#### `ChatCompletionStreamResponse`

Response chunk from a streaming completion request.

#### `StreamOptions`

Configures how markdown streaming output is rendered.

- `Raw`: When true, writes chunks directly without styling
- `WordWrap`: Wrap width for the renderer (defaults to 120 when zero)
- `Cancel`: Optional callback invoked when the user presses Ctrl+C in the markdown viewer

#### `StreamReader`

Provides access to streaming responses.

### Functions

#### `NewClient(apiKey string, opts ...ClientOption) *Client`

Creates a new OpenAI client.

**Parameters:**

- `apiKey`: Your OpenAI API key
- `opts`: Optional configuration options

**Example:**

```go
client := openai.NewClient(apiKey)
```

#### `CreateChatCompletion(ctx context.Context, req ChatCompletionRequest) (string, error)`

Sends a non-streaming chat completion request and returns the complete response.

**Parameters:**

- `ctx`: Context for the request
- `req`: The chat completion request configuration

**Returns:**

- `string`: The assistant's response content
- `error`: Any error that occurred

#### `CreateChatCompletionStream(ctx context.Context, req ChatCompletionRequest) (*StreamReader, error)`

Sends a streaming chat completion request.

**Parameters:**

- `ctx`: Context for the request
- `req`: The chat completion request configuration

**Returns:**

- `*StreamReader`: A stream reader for receiving chunks
- `error`: Any error that occurred

#### `CreateChatCompletionStreamWithMarkdown(ctx context.Context, req ChatCompletionRequest, w io.Writer, opts StreamOptions) error`

Sends a streaming chat completion request and renders the incremental markdown output to the provided writer.

**Parameters:**

- `ctx`: Context for the request
- `req`: The chat completion request configuration
- `w`: Destination for the rendered markdown (for example, `os.Stdout`)
- `opts`: Rendering options that control wrapping, raw output, and cancellation

**Returns:**

- `error`: Any error that occurred while streaming or rendering

#### `StreamReader.Recv() (ChatCompletionStreamResponse, error)`

Reads the next chunk from the stream.

**Returns:**

- `ChatCompletionStreamResponse`: The next chunk
- `error`: `io.EOF` when the stream ends, or any other error

#### `StreamReader.Close() error`

Closes the stream. Should be called when done reading.

### Options

#### `WithHTTPClient(httpClient *http.Client) ClientOption`

Sets a custom HTTP client.

**Example:**

```go
httpClient := &http.Client{Timeout: 60 * time.Second}
client := openai.NewClient(apiKey, openai.WithHTTPClient(httpClient))
```

## Error Handling

The package returns detailed errors for various failure scenarios:

- Network errors
- API errors (with status code and message)
- JSON parsing errors
- Empty responses

Always check for errors:

```go
response, err := client.CreateChatCompletion(ctx, req)
if err != nil {
    log.Printf("Error: %v", err)
    return
}
```
