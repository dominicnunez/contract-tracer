package sample

import (
	"context"
	"time"
)

func CancellationWorker(ctx context.Context, finished chan<- struct{}) {
	defer close(finished)
	select {
	case <-ctx.Done():
		return
	}
}
func CancellationStart() {
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go CancellationWorker(ctx, finished)
	cancel()
	<-finished
}
func WaitSignal(done <-chan struct{}) { <-done }
func WrappedDone(ctx context.Context) <-chan struct{} {
	return context.WithValue(ctx, "key", "value").Done()
}
func WrappedDoneStart() {
	ctx, cancel := context.WithCancel(context.Background())
	go WaitSignal(WrappedDone(ctx))
	cancel()
}
func NilDoneReceive() { <-context.Background().Done() }
func DetachedDone(parent context.Context) {
	child := context.WithoutCancel(parent)
	WaitSignal(child.Done())
}

func NilDoneSelect() {
	select {
	case <-context.TODO().Done():
		return
	default:
		return
	}
}
func DetachStart() {
	ctx, cancel := context.WithCancel(context.Background())
	DetachedDone(ctx)
	cancel()
}

func WrappedNilDone() {
	<-context.WithValue(context.TODO(), "key", "value").Done()
}

func DeadlineChild() {
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	child, cancelChild := context.WithTimeout(parent, time.Second)
	defer cancelChild()
	WaitSignal(child.Done())
}
