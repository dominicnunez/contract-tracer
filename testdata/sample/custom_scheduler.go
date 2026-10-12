package sample

import (
	"context"
	"time"
)

type customSchedulingContext struct {
	context.Context
	ready, stopped chan struct{}
	pending        func()
}

func (ctx *customSchedulingContext) Done() <-chan struct{} { return ctx.ready }
func (ctx *customSchedulingContext) AfterFunc(callback func()) func() bool {
	ctx.pending = callback
	return ctx.stop
}
func (ctx *customSchedulingContext) stop() bool { close(ctx.stopped); return true }
func (ctx *customSchedulingContext) fire() {
	ctx.pending()
	defer ctx.pending()
	go ctx.pending()
}

func CustomSchedulingCaller() {
	base, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	stopped := make(chan struct{})
	ctx := &customSchedulingContext{Context: base, ready: make(chan struct{}), stopped: stopped}
	stop := context.AfterFunc(ctx, func() { close(finished) })
	defer stop()
	ctx.fire()
	cancel()
	<-finished
}

func ChildCustomSchedulingCaller() {
	parent := &customSchedulingContext{Context: context.Background(), ready: make(chan struct{})}
	child, cancel := context.WithCancel(parent)
	parent.fire()
	cancel()
	<-child.Done()
}

func OtherChildCustomSchedulingCaller() {
	parent := &customSchedulingContext{Context: context.Background(), ready: make(chan struct{})}
	_, cause := context.WithCancelCause(parent)
	_, deadline := context.WithDeadline(parent, time.Now())
	_, deadlineCause := context.WithDeadlineCause(parent, time.Now(), nil)
	_, timeout := context.WithTimeout(parent, time.Hour)
	_, timeoutCause := context.WithTimeoutCause(parent, time.Hour, nil)
	parent.fire()
	cause(nil)
	deadline()
	deadlineCause()
	timeout()
	timeoutCause()
}

type namedCallback func()
type namedSchedulingContext struct{ context.Context }

func (*namedSchedulingContext) AfterFunc(namedCallback) func() bool {
	return func() bool { return true }
}
func namedSchedulingCallback() {}

type promotedSchedulingContext struct{ *customSchedulingContext }

type valuePromotedSchedulingContext struct{ customSchedulingContext }
type nestedPromotedSchedulingContext struct {
	*valuePromotedSchedulingContext
}

func valuePromotedSchedulingCallback() {}
func ValuePromotedSchedulingCaller() {
	stopped := make(chan struct{})
	outer := &valuePromotedSchedulingContext{customSchedulingContext{Context: context.Background(), ready: make(chan struct{}), stopped: stopped}}
	stop := context.AfterFunc(outer, valuePromotedSchedulingCallback)
	stop()
	outer.fire()
}

func nestedPromotedSchedulingCallback() {}
func NestedPromotedSchedulingCaller() {
	stopped := make(chan struct{})
	inner := &valuePromotedSchedulingContext{customSchedulingContext{Context: context.Background(), ready: make(chan struct{}), stopped: stopped}}
	outer := &nestedPromotedSchedulingContext{inner}
	stop := context.AfterFunc(outer, nestedPromotedSchedulingCallback)
	stop()
	outer.fire()
}

type valueReceiverSchedulingContext struct {
	context.Context
	stopped chan struct{}
}

func (ctx valueReceiverSchedulingContext) AfterFunc(func()) func() bool {
	return func() bool { close(ctx.stopped); return true }
}
func valueReceiverSchedulingCallback() {}
func ValueReceiverSchedulingCaller() {
	stopped := make(chan struct{})
	ctx := valueReceiverSchedulingContext{Context: context.Background(), stopped: stopped}
	stop := context.AfterFunc(ctx, valueReceiverSchedulingCallback)
	stop()
}

func promotedSchedulingCallback() {}
func PromotedSchedulingCaller() {
	stopped := make(chan struct{})
	inner := &customSchedulingContext{Context: context.Background(), ready: make(chan struct{}), stopped: stopped}
	outer := &promotedSchedulingContext{inner}
	stop := context.AfterFunc(outer, promotedSchedulingCallback)
	stop()
	outer.fire()
}
func NamedSchedulingCaller() {
	stop := context.AfterFunc(&namedSchedulingContext{context.Background()}, namedSchedulingCallback)
	stop()
}

type wrongSchedulingContext struct{ context.Context }

func (*wrongSchedulingContext) AfterFunc(func()) func() error { return func() error { return nil } }
func wrongSchedulingCallback()                                {}
func WrongSchedulingCaller() {
	stop := context.AfterFunc(&wrongSchedulingContext{context.Background()}, wrongSchedulingCallback)
	stop()
}

func OutsideCustomScheduling(ctx context.Context) { context.AfterFunc(ctx, outsideSchedulingCallback) }
func outsideSchedulingCallback()                  {}
func KnownOutsideCustomSchedulingCaller() {
	OutsideCustomScheduling(&customSchedulingContext{Context: context.Background(), ready: make(chan struct{})})
}
