package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/jiyeol-lee/openai"
)

func main() {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		log.Fatal("OPENAI_API_KEY environment variable not set")
	}

	client := openai.NewClient(apiKey)

	req := openai.ChatCompletionRequest{
		Model: "gpt-5-nano",
		Messages: []openai.Message{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: "Tell me a long joke."},
		},
		Temperature:     1,
		ReasoningEffort: "minimal",
	}

	stream, err := client.CreateChatCompletionStream(context.Background(), req)
	if err != nil {
		log.Fatal(err)
	}
	defer stream.Close()

	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatal(err)
		}

		if len(chunk.Choices) > 0 {
			fmt.Print(chunk.Choices[0].Delta.Content)
		}
	}
	fmt.Println()
}
