package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/jiyeol-lee/openai"
)

func main() {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		log.Fatal("OPENAI_API_KEY environment variable not set")
	}

	client := openai.NewClient(apiKey)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	req := openai.ChatCompletionRequest{
		Model: "gpt-4o-mini",
		Messages: []openai.Message{
			{Role: "system", Content: "You are a go programming assistant."},
			{
				Role:    "user",
				Content: "Explain the goroutines in Go programming language with examples in markdown format.",
			},
		},
		Temperature: 0.8,
	}

	opts := openai.StreamOptions{
		WordWrap: 100,
		Cancel: func() {
			log.Println("stream cancelled")
		},
	}

	if err := client.CreateChatCompletionStreamWithMarkdown(ctx, req, os.Stdout, opts); err != nil {
		log.Fatalf("stream error: %v", err)
	}
}
