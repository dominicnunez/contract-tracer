package sample

import (
	"database/sql"
	"sync"
)

var PublicReplaceableFunction func()

// The private callback has a known package initializer so this fixture tests
// visibility, not the real zero-value-before-first-write behavior.
var PublicFunctionRecord = struct {
	Call   func()
	hidden func()
}{hidden: knownMixedFunction}
var PublicFunctionSlice []func()
var PublicFunctionMap map[string]func()

func PublicInvokeFunction(callback func()) { forwardOpenFunction(callback) }
func forwardOpenFunction(callback func())  { callback(); defer callback(); go callback() }
func closedInvokeFunction(callback func()) { callback() }
func knownMixedFunction()                  {}

func MixedFunctionCaller() {
	PublicInvokeFunction(knownMixedFunction)
	closedInvokeFunction(knownMixedFunction)
	PublicReplaceableFunction = knownMixedFunction
	PublicFunctionRecord.Call = knownMixedFunction
	PublicFunctionRecord.hidden = knownMixedFunction
	PublicFunctionSlice = []func(){knownMixedFunction}
	PublicFunctionMap = map[string]func(){"local": knownMixedFunction}
	readPublicFunctions()
}

func readPublicFunctions() {
	PublicReplaceableFunction()
	PublicFunctionRecord.Call()
	PublicFunctionSlice[0]()
	PublicFunctionMap["local"]()
}
func readPrivateFunctionField() { PublicFunctionRecord.hidden() }

func MixedDependencyFunction(choose bool) {
	stop := localFunctionStop
	if choose {
		stop = sync.OnceValue(localFunctionStop)
	}
	stop()
}
func localFunctionStop() bool { return true }

func PublicCallFactory(factory func() func())  { consumeOpenFactory(factory) }
func consumeOpenFactory(factory func() func()) { factory()() }
func closedFunctionFactory() func()            { return knownMixedFunction }
func MixedFactoryCaller()                      { PublicCallFactory(closedFunctionFactory) }

func PublicDispatchFunction(dispatch func(func(*sql.DB))) { dispatch(mixedDispatchQuery) }
func localDispatchFunction(callback func(*sql.DB))        { callback(OpenOrdersDatabase()) }
func MixedDispatchCaller()                                { PublicDispatchFunction(localDispatchFunction) }
func mixedDispatchQuery(db *sql.DB)                       { db.Query("SELECT body FROM mixed_dispatch_records") }
