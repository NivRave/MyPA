// Command listmodels prints the Gemini models available to GEMINI_API_KEY (read from the
// environment or a local .env). Developer utility: `go run ./cmd/devtools/listmodels`.
// Not built into any container image.
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
	client, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: apiKey})
	if err != nil {
		log.Fatalf("failed to create client: %v", err)
	}

	for m, err := range client.Models.All(ctx) {
		if err != nil {
			log.Fatalf("failed to list models: %v", err)
		}
		fmt.Println(m.Name)
	}
}
