package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/jiyeol-lee/openai/internal"
)

// Message represents a single message in a chat conversation
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatCompletionRequest represents a chat completion request
type ChatCompletionRequest struct {
	Model           string    `json:"model"`
	Messages        []Message `json:"messages"`
	Temperature     float32   `json:"temperature,omitempty"`
	ReasoningEffort string    `json:"reasoning_effort,omitempty"`
	Stream          bool      `json:"stream,omitempty"`
}

// ChatCompletionResponse represents the API response for non-streaming requests
type ChatCompletionResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index        int     `json:"index"`
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// ChatCompletionStreamResponse represents a streaming chunk response
type ChatCompletionStreamResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role    string `json:"role,omitempty"`
			Content string `json:"content,omitempty"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
}

// StreamReader provides access to streaming chat completion responses
type StreamReader struct {
	reader  *bufio.Reader
	closer  io.Closer
	isFirst bool
}

// deferredCloser allows setting and invoking a close function exactly once,
// even when the close request happens before the function is available.
type deferredCloser struct {
	mu     sync.Mutex
	closed bool
	fn     func()
}

// Set registers the close function. If Close was already called the function
// runs immediately.
func (d *deferredCloser) Set(fn func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		if fn != nil {
			fn()
		}
		return
	}
	d.fn = fn
}

// Close executes the registered function exactly once.
func (d *deferredCloser) Close() {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	d.closed = true
	fn := d.fn
	d.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// Recv reads the next chunk from the stream
func (s *StreamReader) Recv() (ChatCompletionStreamResponse, error) {
	var response ChatCompletionStreamResponse

	for {
		line, err := s.reader.ReadBytes('\n')
		if err != nil {
			return response, err
		}

		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		// SSE format: "data: {...}"
		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}

		data := bytes.TrimPrefix(line, []byte("data: "))

		// Check for stream end
		if string(data) == "[DONE]" {
			return response, io.EOF
		}

		if err := json.Unmarshal(data, &response); err != nil {
			return response, fmt.Errorf("failed to decode stream chunk: %w", err)
		}

		return response, nil
	}
}

// Close closes the stream
func (s *StreamReader) Close() error {
	return s.closer.Close()
}

// CreateChatCompletion sends a non-streaming chat completion request
func (c *Client) CreateChatCompletion(
	ctx context.Context,
	req ChatCompletionRequest,
) (string, error) {
	req.Stream = false

	body, err := marshalRequest(req)
	if err != nil {
		return "", err
	}

	resp, err := c.doRequest(ctx, "POST", "/chat/completions", body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var payload ChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	if len(payload.Choices) == 0 {
		return "", fmt.Errorf("no completion choices returned")
	}

	return strings.TrimSpace(payload.Choices[0].Message.Content), nil
}

// CreateChatCompletionStream sends a streaming chat completion request
func (c *Client) CreateChatCompletionStream(
	ctx context.Context,
	req ChatCompletionRequest,
) (*StreamReader, error) {
	req.Stream = true

	body, err := marshalRequest(req)
	if err != nil {
		return nil, err
	}

	resp, err := c.doRequest(ctx, "POST", "/chat/completions", body)
	if err != nil {
		return nil, err
	}

	return &StreamReader{
		reader:  bufio.NewReader(resp.Body),
		closer:  resp.Body,
		isFirst: true,
	}, nil
}

// CreateChatCompletionStreamWithMarkdown sends a streaming chat completion request
// and renders the output incrementally as markdown using the StreamMarkdown function
func (c *Client) CreateChatCompletionStreamWithMarkdown(
	ctx context.Context,
	req ChatCompletionRequest,
	w io.Writer,
	opts StreamOptions,
) error {
	readerCtx, cancelReader := context.WithCancel(ctx)
	defer cancelReader()

	closer := &deferredCloser{}
	pump := c.startChunkPump(readerCtx, req, closer)

	userCancel := opts.Cancel
	opts.Cancel = func() {
		cancelReader()
		closer.Close()
		if userCancel != nil {
			userCancel()
		}
	}

	next := func(nextCtx context.Context) (markdown.Chunk, error) {
		select {
		case <-nextCtx.Done():
			return markdown.Chunk{}, nextCtx.Err()
		case chunk, ok := <-pump.chunks:
			if !ok {
				return markdown.Chunk{}, io.EOF
			}
			return chunk, nil
		}
	}

	uiErr := markdown.StreamMarkdown(ctx, next, w, opts)
	pumpErr := <-pump.done

	if uiErr != nil {
		if errors.Is(uiErr, context.Canceled) && pumpErr != nil {
			return pumpErr
		}
		return uiErr
	}

	return pumpErr
}

// chunkPump holds the channels used to pass chunks to the markdown renderer
// and to report completion/error back to the caller.
type chunkPump struct {
	chunks <-chan markdown.Chunk
	done   <-chan error
}

// startChunkPump spins up a goroutine that reads SSE events from OpenAI and
// forwards only the streamed text into a channel suitable for the markdown
// renderer. It ensures the underlying stream is closed exactly once and that
// errors are propagated through the done channel.
func (c *Client) startChunkPump(
	ctx context.Context,
	req ChatCompletionRequest,
	closer *deferredCloser,
) *chunkPump {
	chunkCh := make(chan markdown.Chunk)
	doneCh := make(chan error, 1)

	go func() {
		defer close(chunkCh)
		var finalErr error
		defer func() { doneCh <- finalErr }()

		stream, err := c.CreateChatCompletionStream(ctx, req)
		if err != nil {
			finalErr = fmt.Errorf("failed to create stream: %w", err)
			return
		}

		closer.Set(func() { stream.Close() })
		defer closer.Close()

		for {
			chunk, recvErr := stream.Recv()
			if recvErr == io.EOF {
				return
			}
			if recvErr != nil {
				finalErr = fmt.Errorf("stream error: %w", recvErr)
				return
			}

			text := extractDeltaText(chunk)
			if text == "" {
				continue
			}

			select {
			case chunkCh <- markdown.Chunk{Text: text}:
			case <-ctx.Done():
				finalErr = ctx.Err()
				return
			}
		}
	}()

	return &chunkPump{
		chunks: chunkCh,
		done:   doneCh,
	}
}

func extractDeltaText(resp ChatCompletionStreamResponse) string {
	if len(resp.Choices) == 0 {
		return ""
	}
	return resp.Choices[0].Delta.Content
}
