package sample

func OtherMapTarget(n int) bool { return n < -1 }
func MapCallback() bool {
	checks := map[string]func(int) bool{"valid": Validate, "other": OtherMapTarget}
	return checks["valid"](1)
}
func receiveKind(kinds <-chan string) Event { return Event{EventType: <-kinds} }
func sendKind(kinds chan<- string)          { kinds <- "CHANNEL_EVENT" }
func ChannelEvent() Event {
	kinds := make(chan string, 1)
	sendKind(kinds)
	result := receiveKind(kinds)
	close(kinds)
	return result
}
func ChannelConsume(e Event) bool { return e.EventType == "CHANNEL_EVENT" }
func selectKind(kinds <-chan string) Event {
	select {
	case kind := <-kinds:
		return Event{EventType: kind}
	default:
		return Event{}
	}
}
func SelectEvent() Event { kinds := make(chan string, 1); sendKind(kinds); return selectKind(kinds) }

func IntegerMapCallback() bool {
	checks := map[int]func(int) bool{1: Validate, 2: OtherMapTarget}
	check, ok := checks[1]
	return ok && check(1)
}
func SecondSelectEvent() Event {
	first, second := make(chan string, 1), make(chan string, 1)
	first <- "FIRST_CHANNEL"
	second <- "SECOND_CHANNEL"
	select {
	case <-first:
		return Event{}
	case kind := <-second:
		return Event{EventType: kind}
	}
}
func CommaReceiveEvent() Event {
	channel := make(chan string, 1)
	channel <- "COMMA_EVENT"
	kind, ok := <-channel
	if !ok {
		return Event{}
	}
	return Event{EventType: kind}
}
