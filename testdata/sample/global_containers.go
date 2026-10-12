package sample

var privateMutationMap = map[string]string{"initial": "value"}
var privateMutationSlice = []string{"initial"}
var unrelatedMutationMap = map[string]string{}

func GlobalContainerCaller() {
	mutateSharedMap(privateMutationMap)
	readSharedMap(privateMutationMap)
	mutateSharedSlice(privateMutationSlice)
	readSharedSlice(privateMutationSlice)
	defer copy(privateMutationSlice, []string{"deferred"})
	go copy(privateMutationSlice, []string{"asynchronous"})
}

func mutateSharedMap(state map[string]string) {
	state["new"] = "value"
	delete(state, "initial")
	clear(state)
}

func readSharedMap(state map[string]string) string {
	value := state["new"]
	for key := range state {
		value += key
	}
	return value
}

func mutateSharedSlice(state []string) {
	state[0] = "indexed"
	copy(state, []string{"copied"})
	_ = append(state, "appended")
	clear(state)
}

func readSharedSlice(state []string) string {
	copy(make([]string, len(state)), state)
	return state[0]
}

func mutateUnrelatedMap() { unrelatedMutationMap["unrelated"] = "value" }
