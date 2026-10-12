package sample

import "context"

func CleanupBranches(skip, early bool) {
	if skip {
		return
	}
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan struct{})
	defer close(finished)
	ordinary := make(chan struct{})
	close(ordinary)
	if early {
		return
	}
	panic("cleanup path")
}

func CleanupHelper(cancel context.CancelFunc, done chan struct{}) {
	cancel()
	close(done)
}

func DeferredHelper() {
	_, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	defer CleanupHelper(cancel, done)
}

func DeferredLoop() {
	defer func() {}()
	for {
	}
}
