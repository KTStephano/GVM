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
	divByZeroTest1 = `
		const 0
		const 1
		divi
	`

	divByZeroTest2 = `
		const 0
		const 1
		remu
	`

	divByZeroTest3 = `
		const 0
		const 1
		rems
	`

	stackOverflowTest = `
	loop:	
		const 5
		jmp loop
	`

	illegalInstrTest = `
		const 1
		srstore 32	        // set CPU mode to non-privileged
		write 0 0
	`

	unknownInstrTest = `
		const 0xFFFFFFFF
		const 0x00
		storep32			// store max uint at mem location 0x00
		jmp 0x00			// jump to instruction at mem location 0x00
	`

	errIOTest = `
	loop:
		// set up a character input request from console IO device
		const 0             // no input data
		const 0             // unused interaction id
		write 3 4           // port 3 = console IO device, command 4 = read 32-bit character
		pop 4               // get rid of write result
		jmp loop
	`
)

func TestVM(t *testing.T) {
	vm := compileAndCheck(t, "../examples/poweroff.b")
	runAndEnsureSpecificShutdown(t, vm, errSystemShutdown)

	vm = compileAndCheck(t, "../examples/runtime.b", "../examples/loop.b")
	runAndEnsureSpecificShutdown(t, vm, errSystemShutdown)

	vm = compileAndCheck(t, "../examples/runtime.b", "../examples/helloworld.b")
	runAndEnsureSpecificShutdown(t, vm, errSystemShutdown)

	vm = compileAndCheckSource(t, divByZeroTest1)
	runAndEnsureSpecificShutdown(t, vm, errDivisionByZero)

	vm = compileAndCheckSource(t, divByZeroTest2)
	runAndEnsureSpecificShutdown(t, vm, errDivisionByZero)

	vm = compileAndCheckSource(t, divByZeroTest3)
	runAndEnsureSpecificShutdown(t, vm, errDivisionByZero)

	vm = compileAndCheckSource(t, stackOverflowTest)
	runAndEnsureSpecificShutdown(t, vm, errSegmentationFault)

	vm = compileAndCheckSource(t, illegalInstrTest)
	runAndEnsureSpecificShutdown(t, vm, errIllegalInstruction)

	vm = compileAndCheckSource(t, unknownInstrTest)
	runAndEnsureSpecificShutdown(t, vm, errUnknownInstruction)

	vm = compileAndCheckSource(t, errIOTest)
	runAndEnsureSpecificShutdown(t, vm, errIO)
}
