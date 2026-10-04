package sample

import (
	"context"
	"sync"
	"time"
)

func summaryStopTarget() {}
func SummaryEscapeCaller() {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(time.Hour, cancel)
	defer time.AfterFunc(time.Hour, cancel)
	go time.AfterFunc(time.Hour, cancel)
	stop := context.AfterFunc(ctx, summaryStopTarget)
	sync.OnceValue(stop)
}

type LifecycleSummaries struct {
	Cancel context.CancelFunc
	Stop   func() bool
}

func returnedSummaryTarget() {}
func PublicLifecycleSummaries() LifecycleSummaries {
	ctx, cancel := context.WithCancel(context.Background())
	return LifecycleSummaries{Cancel: cancel, Stop: context.AfterFunc(ctx, returnedSummaryTarget)}
}

func makeGlobalCancellationSummary() context.CancelFunc {
	_, cancel := context.WithCancel(context.Background())
	return cancel
}

var PublicCancellationSummary = makeGlobalCancellationSummary()

type escapingSchedulingContext struct {
	context.Context
	ready chan struct{}
}

func (ctx *escapingSchedulingContext) Done() <-chan struct{} { return ctx.ready }
func (*escapingSchedulingContext) AfterFunc(callback func()) func() bool {
	return time.AfterFunc(time.Hour, callback).Stop
}
func escapingSchedulerTarget() {}
func EscapingSchedulingCaller() {
	ctx := &escapingSchedulingContext{Context: context.Background(), ready: make(chan struct{})}
	context.AfterFunc(ctx, escapingSchedulerTarget)
}
