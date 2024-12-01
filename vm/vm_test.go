package gvm

import (
	"fmt"
	"strings"
	"testing"
)

func assert(t *testing.T, cond bool, format string, args ...any) {
	if !cond {
		t.Fatalf(fmt.Sprintf("%v %s", cond, format), args...)
	}
}

func compileAndCheckSource(t *testing.T, source string) *VM {
	instrs, err := CompileSourceFromBuffer(false, strings.Split(source, "\n"))
	assert(t, err == nil, "Failed to compile: %s", err)

	vm := NewVirtualMachine(instrs)
	assert(t, vm != nil, "Failed to create new VM")
	return vm
}

func compileAndCheck(t *testing.T, files ...string) *VM {
	instrs, err := CompileSource(false, files...)
	assert(t, err == nil, "Failed to compile: %s", err)

	vm := NewVirtualMachine(instrs)
	assert(t, vm != nil, "Failed to create new VM")
	return vm
}

func runAndEnsureSpecificShutdown(t *testing.T, vm *VM, errcode error) {
	vm.RunProgram()
	assert(t, vm.errcode == errcode, "Got unexpected error code after running VM: %s", vm.errcode)
}

var (
	divByZeroTest = `
		const 0
		const 1
		divi
	`

	stackOverflowTest = `
	loop:	
		const 5
		jmp loop
	`

	illegalInstrTest = `
		const 1
		srstore 32	// set CPU mode to non-privileged
		write 0 0
	`

	unknownInstrTest = `
		const 0xFFFFFFFF
		const 0x00
		storep32
		jmp 0x00
	`
)

func TestVM(t *testing.T) {
	vm := compileAndCheck(t, "../examples/poweroff.b")
	runAndEnsureSpecificShutdown(t, vm, errSystemShutdown)

	vm = compileAndCheck(t, "../examples/runtime.b", "../examples/loop.b")
	runAndEnsureSpecificShutdown(t, vm, errSystemShutdown)

	vm = compileAndCheck(t, "../examples/runtime.b", "../examples/helloworld.b")
	runAndEnsureSpecificShutdown(t, vm, errSystemShutdown)

	vm = compileAndCheckSource(t, divByZeroTest)
	runAndEnsureSpecificShutdown(t, vm, errDivisionByZero)

	vm = compileAndCheckSource(t, stackOverflowTest)
	runAndEnsureSpecificShutdown(t, vm, errSegmentationFault)

	vm = compileAndCheckSource(t, illegalInstrTest)
	runAndEnsureSpecificShutdown(t, vm, errIllegalInstruction)

	vm = compileAndCheckSource(t, unknownInstrTest)
	runAndEnsureSpecificShutdown(t, vm, errUnknownInstruction)
}
