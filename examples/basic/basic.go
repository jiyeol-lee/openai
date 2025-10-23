package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/jiyeol-lee/openai"
)

func main() {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		log.Fatal("OPENAI_API_KEY environment variable not set")
	}

	// Create a new OpenAI client
	client := openai.NewClient(apiKey)

	// Create a chat completion request
	req := openai.ChatCompletionRequest{
		Model: "gpt-5",
		Messages: []openai.Message{
			{Role: "system", Content: "You are a helpful assistant that provides concise answers."},
			{Role: "user", Content: "What is the capital of France?"},
		},
		Temperature:     1,
		ReasoningEffort: "minimal",
	}

	// Get the completion
	response, err := client.CreateChatCompletion(context.Background(), req)
	if err != nil {
		log.Fatalf("Error: %v", err)
	}

	fmt.Println("Response:", response)
}
