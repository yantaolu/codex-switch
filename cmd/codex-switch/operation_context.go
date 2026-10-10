package main

import (
	"context"
	"os"
	"os/signal"

	"codex-switch/internal/switcher"
)

// Outside a Tea page, native login runs in its own process group. Intercepting
// SIGINT lets context cancellation kill that group and run storage cleanup.
func interruptedOperation(e switcher.Engine, execute func(switcher.Engine) error) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	return execute(e.WithContext(ctx))
}
