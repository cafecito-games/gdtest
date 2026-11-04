// Command gdtest runs GDScript tests through a bounded local orchestration service.
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/cafecito-games/gdtest/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(app.Run(ctx, os.Args[1:], app.IO{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}))
}
