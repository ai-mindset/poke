package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/ai-mindset/poke/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := cli.New(os.Stdout, os.Stderr).Run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
