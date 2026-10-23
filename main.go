package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

func main() {
	steps := flag.Int("steps", 8, "maximum number of agent steps")
	flag.Parse()

	topic := strings.TrimSpace(strings.Join(flag.Args(), " "))
	if topic == "" {
		fmt.Println(`Usage: go run . [-steps N] "your research topic"`)
		os.Exit(1)
	}

	log.SetFlags(0) // progress logs go to stderr, the final answer goes to stdout

	// Load settings from .env (real environment variables take priority).
	if err := loadDotEnv(".env"); err != nil {
		log.Fatalf("could not read .env: %v", err)
	}
	if os.Getenv("GEMINI_API_KEY") == "" {
		log.Fatal("GEMINI_API_KEY is not set.\n" +
			"Copy .env.example to .env and add your key (free key: https://aistudio.google.com/apikey)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	agent := NewAgent([]Tool{searchTool{}, fetchTool{}}, *steps)

	answer, err := agent.Run(ctx, "Research this topic: "+topic)
	if err != nil {
		log.Fatalf("error: %v", err)
	}
	fmt.Println("\n" + answer)
}
