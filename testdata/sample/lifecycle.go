package sample

import "context"

func Worker(ctx context.Context) { <-ctx.Done() }
func Start(ctx context.Context) { child, cancel := context.WithCancel(ctx); defer cancel(); go Worker(child) }
func newContext(ctx context.Context) (context.Context,context.CancelFunc) { return context.WithCancel(ctx) }
func WrapperStart(ctx context.Context) { child, stop := newContext(ctx); defer stop(); go Worker(child) }
func Unreleased(ctx context.Context) { child, _ := context.WithCancel(ctx); Worker(child) }
func DualContexts(ctx context.Context) { _, first := context.WithCancel(ctx); _, second := context.WithCancel(ctx); first(); _ = second }
