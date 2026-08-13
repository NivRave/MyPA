package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/joho/godotenv"
	"google.golang.org/genai"
)

func main() {
	_ = godotenv.Load()
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		log.Fatal("GEMINI_API_KEY not set")
	}

	ctx := context.Background()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey: apiKey,
	})
	if err != nil {
		log.Fatalf("failed to create client: %v", err)
	}

	// List models
	pager := client.Models.List(ctx, nil)
	for {
		models, err := pager.NextPage()
		if err != nil {
			log.Fatalf("failed to list models: %v", err)
		}
		if len(models) == 0 {
			break
		}
		for _, m := range models {
			fmt.Println(m.Name)
		}
	}
}
