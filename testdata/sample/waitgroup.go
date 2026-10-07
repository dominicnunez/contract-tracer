package sample

import "sync"

func GroupWorker(group *sync.WaitGroup) { defer group.Done() }
func GroupWait(group *sync.WaitGroup)   { group.Wait() }
func GroupStart() {
	var group, unrelated sync.WaitGroup
	group.Add(1)
	go GroupWorker(&group)
	GroupWait(&group)
	unrelated.Add(2)
	unrelated.Done()
	unrelated.Wait()
}

func ManagedTask() {}
func GroupGo() {
	var group sync.WaitGroup
	group.Go(ManagedTask)
	group.Wait()
}

var GlobalGroup sync.WaitGroup

type GroupOwner struct{ workers, other sync.WaitGroup }

func GroupFields() {
	var owner GroupOwner
	owner.workers.Add(1)
	GroupWorker(&owner.workers)
	owner.other.Add(-1)
	GlobalGroup.Add(2)
	GroupWait(&GlobalGroup)
}

func UnknownGroup(group *sync.WaitGroup) { group.Wait() }

func BoundCompletion(done func()) { defer done() }
func BoundJoin(wait func())       { wait() }
func BoundGroup() {
	var group, unrelated sync.WaitGroup
	group.Add(1)
	unrelated.Add(2)
	BoundCompletion(group.Done)
	BoundJoin(group.Wait)
	finishOther := unrelated.Done
	finishOther()
}

func BoundGroupGo() {
	var group sync.WaitGroup
	add := group.Add
	add(0)
	start := group.Go
	start(ManagedTask)
	group.Wait()
}

func BoundGroupChoices(wait bool) {
	var first, second sync.WaitGroup
	first.Add(1)
	second.Add(2)
	selected := first.Done
	if wait {
		selected = second.Wait
	}
	defer selected()
}

type GroupCompletion interface{ Done() }
type GroupJoin interface{ Wait() }

func InterfaceCompletion(group GroupCompletion) { defer group.Done() }
func InterfaceJoin(group GroupJoin)             { group.Wait() }
func InterfaceBound(group GroupCompletion, join GroupJoin) {
	BoundCompletion(group.Done)
	BoundJoin(join.Wait)
}

type OtherCompletion struct{}

func (*OtherCompletion) Done() {}
func InterfaceGroups() {
	var group sync.WaitGroup
	group.Add(1)
	InterfaceCompletion(&group)
	InterfaceJoin(&group)
	InterfaceBound(&group, &group)
	InterfaceCompletion(&OtherCompletion{})
}

type ValueGroup struct{ sync.WaitGroup }
type PointerGroup struct{ *ValueGroup }
type NestedGroup struct{ PointerGroup }

type DirectPointerGroup struct{ *sync.WaitGroup }
type DirectNestedGroup struct{ DirectPointerGroup }

type ClosedGroup struct{ sync.WaitGroup }

func SimplePromotedMethodExpressions() {
	group := &DirectNestedGroup{DirectPointerGroup: DirectPointerGroup{WaitGroup: &sync.WaitGroup{}}}
	group.WaitGroup.Add(2)
	(*DirectNestedGroup).Add(group, 1)
}

func closedGroupWait(wait func(*ClosedGroup), group *ClosedGroup) { wait(group) }
func ClosedWaitGroupHelper() {
	group := &ClosedGroup{}
	closedGroupWait((*ClosedGroup).Wait, group)
}

func MethodExpressionAdd(group *NestedGroup, delta int)         { (*NestedGroup).Add(group, delta) }
func MethodExpressionDone(group *NestedGroup)                   { done := (*NestedGroup).Done; go done(group) }
func MethodExpressionWait(group *NestedGroup)                   { wait := (*NestedGroup).Wait; defer wait(group) }
func callGroupWait(wait func(*NestedGroup), group *NestedGroup) { wait(group) }
func MethodExpressionHelper(group *NestedGroup)                 { callGroupWait((*NestedGroup).Wait, group) }

func callMixedGroupMethod(method func(*NestedGroup), group *NestedGroup) { method(group) }
func MixedMethodExpressionRoles() {
	group := &NestedGroup{PointerGroup: PointerGroup{ValueGroup: &ValueGroup{}}}
	group.WaitGroup.Add(1)
	callMixedGroupMethod((*NestedGroup).Done, group)
	callMixedGroupMethod((*NestedGroup).Wait, group)
}

type UserDone struct{}

func (*UserDone) Done()                          {}
func SameNameNonWaitGroupMethod(group *UserDone) { (*UserDone).Done(group) }

func knownWaitGroupTask() {}
func PublicMethodExpressionGo(flag bool, outside func()) {
	group := &sync.WaitGroup{}
	task := knownWaitGroupTask
	if flag {
		task = outside
	}
	(*sync.WaitGroup).Go(group, task)
}

func PublicMethodExpressionGoNil(flag bool) {
	group := &sync.WaitGroup{}
	task := knownWaitGroupTask
	if flag {
		task = nil
	}
	(*sync.WaitGroup).Go(group, task)
}

func PromotedMethodExpressionSites(flag bool, outside *NestedGroup) {
	known := &NestedGroup{PointerGroup: PointerGroup{ValueGroup: &ValueGroup{}}}
	unrelated := &NestedGroup{PointerGroup: PointerGroup{ValueGroup: &ValueGroup{}}}
	known.WaitGroup.Add(7)
	add := (*NestedGroup).Add
	add(known, 1)
	done := (*NestedGroup).Done
	go done(known)
	wait := (*NestedGroup).Wait
	defer wait(known)
	callGroupWait((*NestedGroup).Wait, known)
	unrelated.WaitGroup.Add(3)
	selected := known
	if flag {
		selected = outside
	}
	(*NestedGroup).Done(selected)
}

func PromotedMethodExpressions(flag bool, outside *NestedGroup) {
	known := &NestedGroup{PointerGroup: PointerGroup{ValueGroup: &ValueGroup{}}}
	known.WaitGroup.Add(1)
	selected := known
	if flag {
		selected = outside
	}
	(*NestedGroup).Done(selected)
}

func PublicWaitGroupInput(group *sync.WaitGroup) {
	group.Add(1)
	wait := group.Wait
	wait()
}

func uncalledWaitGroupInput(group *sync.WaitGroup) { group.Done() }

func EscapingWaitGroupCallback() func(*sync.WaitGroup) {
	return func(group *sync.WaitGroup) { (*sync.WaitGroup).Done(group) }
}

var PublicGlobalWaitGroup sync.WaitGroup

func PublicGlobalWaitGroupSites() {
	PublicGlobalWaitGroup.Add(1)
	(*sync.WaitGroup).Done(&PublicGlobalWaitGroup)
}
