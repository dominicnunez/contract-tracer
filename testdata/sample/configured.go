package sample

type Message struct { Topic string }
func CustomPublish() Message { return Message{Topic: "order.finished"} }
func CustomConsume(m Message) bool { return m.Topic == "order.finished" }
// This function is deliberately excluded on normal builds.
