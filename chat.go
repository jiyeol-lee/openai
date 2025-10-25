package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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
	// Context for controlling the reader goroutine
	readerCtx, cancelReader := context.WithCancel(ctx)
	defer cancelReader()

	stream, err := c.CreateChatCompletionStream(readerCtx, req)
	if err != nil {
		return fmt.Errorf("failed to create stream: %w", err)
	}
	var closeStream sync.Once
	closeStreamFunc := func() {
		closeStream.Do(func() {
			_ = stream.Close()
		})
	}
	defer closeStreamFunc()

	// Ensure UI interrupts also stop the reader and underlying stream
	userCancel := opts.Cancel
	opts.Cancel = func() {
		cancelReader()
		closeStreamFunc()
		if userCancel != nil {
			userCancel()
		}
	}

	chunkBuf := make(chan markdown.Chunk)
	streamErr := make(chan error, 1)
	go func() {
		defer close(chunkBuf)
		var err error
		defer func() {
			streamErr <- err
		}()
		for {
			chunk, recvErr := stream.Recv()
			if recvErr == io.EOF {
				return
			}
			if recvErr != nil {
				err = fmt.Errorf("stream error: %w", recvErr)
				return
			}

			if len(chunk.Choices) == 0 || chunk.Choices[0].Delta.Content == "" {
				continue
			}

			select {
			case chunkBuf <- markdown.Chunk{Text: chunk.Choices[0].Delta.Content}:
			case <-readerCtx.Done():
				err = readerCtx.Err()
				return
			}
		}
	}()

	next := func(nextCtx context.Context) (markdown.Chunk, error) {
		select {
		case <-nextCtx.Done():
			return markdown.Chunk{}, nextCtx.Err()
		case chunk, ok := <-chunkBuf:
			if !ok {
				err := <-streamErr
				if err == nil {
					return markdown.Chunk{}, io.EOF
				}
				return markdown.Chunk{}, err
			}
			return chunk, nil
		}
	}

	// Render the markdown stream
	if err := markdown.StreamMarkdown(ctx, next, w, opts); err != nil {
		return err
	}

	return nil
}
