package sample

import "context"

type CallbackStop struct{ Stop func() bool }

func registerCancellationCallback(ctx context.Context, callback func()) func() bool {
	return context.AfterFunc(ctx, callback)
}
func stopCancellationCallback(record CallbackStop) { record.Stop() }
func firstCancellationCallback()                   {}
func secondCancellationCallback()                  {}
func detachedCancellationCallback()                {}
func outsideCancellationCallback()                 {}
func OutsideCancellationCallback(ctx context.Context, callback func()) {
	context.AfterFunc(ctx, callback)
}
func AfterFuncCaller() {
	ctx, cancel := context.WithCancel(context.Background())
	first := context.AfterFunc(ctx, firstCancellationCallback)
	second := context.AfterFunc(ctx, secondCancellationCallback)
	stopCancellationCallback(CallbackStop{Stop: first})
	defer second()
	third := registerCancellationCallback(context.WithoutCancel(ctx), detachedCancellationCallback)
	go third()
	OutsideCancellationCallback(ctx, outsideCancellationCallback)
	defer context.AfterFunc(ctx, deferredRegistrationCallback)
	go context.AfterFunc(ctx, goroutineRegistrationCallback)
	cancel()
}

func deferredRegistrationCallback()  {}
func goroutineRegistrationCallback() {}
func AfterFuncCompletionCaller() {
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { close(finished) })
	cancel()
	if !stop() {
		<-finished
	}
}

func MixedKnownStop(choose bool) {
	stop := localFunctionStop
	if choose {
		stop = context.AfterFunc(context.Background(), func() {})
	}
	stop()
}
func AfterFuncCancelBridge() {
	lifetime, endLifetime := context.WithCancel(context.Background())
	child, cancelChild := context.WithCancel(context.Background())
	stop := context.AfterFunc(lifetime, cancelChild)
	stop()
	endLifetime()
	_ = child
}

type cancellationOwner struct{ finished chan struct{} }

func (owner *cancellationOwner) finish() { close(owner.finished) }

func registerAliasedCancellation(register func(context.Context, func()) func() bool, ctx context.Context, callback func()) func() bool {
	return register(ctx, callback)
}

func AliasedCancellationCaller() {
	ctx, cancel := context.WithCancel(context.Background())
	owner := &cancellationOwner{finished: make(chan struct{})}
	stop := registerAliasedCancellation(context.AfterFunc, ctx, owner.finish)
	registerAliasedVoid(context.AfterFunc, ctx, owner.finish)
	cancel()
	stop()
	<-owner.finished
}

func registerAliasedVoid(register func(context.Context, func()) func() bool, ctx context.Context, callback func()) {
	defer register(ctx, callback)
	go register(ctx, callback)
}

func localCancellationFactory(context.Context, func()) func() bool { return localAliasedStop }
func localAliasedStop() bool                                       { return true }
func mixedAliasedCallback()                                        {}

func MixedAliasedCancellation(choose bool) {
	register := context.AfterFunc
	if choose {
		register = localCancellationFactory
	}
	stop := register(context.Background(), mixedAliasedCallback)
	stop()
}

func OutsideAliasedCancellation(register func(context.Context, func()) func() bool) {
	ctx, cancel := context.WithCancel(context.Background())
	stop := register(ctx, outsideAliasedCallback)
	stop()
	cancel()
}

func outsideAliasedCallback() {}

func KnownOutsideAliasedCancellationCaller() { OutsideAliasedCancellation(context.AfterFunc) }
