package sample

func UnknownFunctionCalls(callback func()) {
	callback()
	defer callback()
	go callback()
	copy(make([]string, 1), []string{"builtin"})
}

func KnownFunctionCalls() {
	callback := func() {}
	callback()
	defer callback()
	go callback()
	copy(make([]string, 1), []string{"builtin"})
}
