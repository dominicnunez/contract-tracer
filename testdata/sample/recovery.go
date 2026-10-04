package sample

func RecoveryDirect()   { _ = recover() }
func RecoveryIndirect() { RecoveryDirect() }
func PanicRecovered() {
	defer RecoveryDirect()
	panic("owned panic")
}
func PanicIndirectRecovery() {
	defer RecoveryIndirect()
	panic("indirect recovery")
}
func PanicBare() { panic("bare panic") }

func PanicClosureRecovery() {
	defer func() { _ = recover() }()
	panic("closure recovery")
}
