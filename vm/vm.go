package gvm

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"sync/atomic"
)

// Each register is just a bit pattern with no concept of
// type (signed, unsigned int or float)
//
// = uint32 so that register is a type alias for uint32 - no casting needed
type register = uint32

// We store these if we want extra debug information
type debugSymbols struct {
	// maps from line num -> source
	source map[int]string
}

// Allows devices to communicate information back to the CPU
type deviceResponseBus struct {
	responses     chan *Response
	responseCount atomic.Int32
}

type VM struct {
	// contains all pubRegisters including special reserved
	registers [numRegisters + numReservedRegisters]register
	// public registers (used for load/store)
	pubRegisters []register
	pc           *register // program counter
	sp           *register // stack pointer (grows down (largest address towards smallest address))
	mode         *register // CPU mode where 0x00 = max privilege, 0x01 = min privilege

	memory [heapSizeBytes]byte
	// activeSegment is a byte slice into the VM's memory
	// At the beginning it points to the entire available memory range, but can be restricted at
	// runtime
	activeSegment []byte

	devices     [maxHWDevices]HardwareDevice
	responseBus *deviceResponseBus

	// For when the stack size has been restricted to a certain region of memory
	stackOffsetBytes uint32

	// Tells us how many bytes the initial loaded program was
	processInstructionBytes uint32

	// Allows vm to read/write to some type of output
	stdout *bufio.Writer
	stdin  *bufio.Reader

	// This gets written to whenever program encounters a normal or critical error
	errcode error

	// Debug flags
	debugOut *strings.Builder
	debugSym *debugSymbols
}

// Constrains to integer 32-bit types
type integer32 interface {
	int32 | uint32
}

// Constrains to integer and floating point 32-bit types
type numeric32 interface {
	integer32 | float32
}

const (
	// 4 bytes since our virtual architecture is 32-bit
	varchBytes   register = 4
	varchBytesx2 register = 2 * varchBytes
	varchBytesx3 register = 3 * varchBytes

	// Reserved bytes are where we store the interrupt vector table
	maxInterrupts           uint32 = 64
	maxHWDevices            uint32 = 16
	maxRestrictedInterrupts uint32 = maxHWDevices + 24
	maxPublicInterrupts     uint32 = maxInterrupts - maxRestrictedInterrupts

	reservedBytes        uint32 = maxInterrupts * varchBytes
	numRegisters         uint32 = 32
	numReservedRegisters uint32 = 8
	heapSizeBytes        uint32 = 65536

	// These are the memory address ranges that the interrupts occupy
	// [0, interruptsAddrRange) -> includes privileged and unprivileged
	interruptsAddrRange uint32 = reservedBytes
	// [0, restrictedInterruptsAddrRange)
	restrictedInterruptsAddrRange uint32 = maxRestrictedInterrupts * varchBytes
	// Lower addresses are occupied by the restricted interrupts
	// [restrictedInterruptsAddrRange, publicInterruptsAddrRange)
	publicInterruptsAddrRange uint32 = restrictedInterruptsAddrRange + maxPublicInterrupts*varchBytes
)

var (
	errSystemShutdown     = errors.New("system poweroff requested")
	errSegmentationFault  = errors.New("segmentation fault")
	errDivisionByZero     = errors.New("division by zero")
	errUnknownInstruction = errors.New("instruction not recognized")
	errIllegelInstruction = errors.New("illegal instruction (privilege too low)")
	errIO                 = errors.New("input-output error")
)

// Push the number of reserved bytes and the length of the process instruction bytes
// as the initial arguments
func (vm *VM) pushInitialArgumentsToStack() {
	vm.pushStack(reservedBytes)
	vm.pushStack(vm.processInstructionBytes)
}

func newDeviceResponseBus() *deviceResponseBus {
	return &deviceResponseBus{
		responses: make(chan *Response, 1),
	}
}

func (bus *deviceResponseBus) Send(resp *Response) {
	bus.responses <- resp
	bus.responseCount.Add(1)
}

func (bus *deviceResponseBus) Ready() bool {
	return bus.responseCount.Load() > 0
}

func (bus *deviceResponseBus) Receive() *Response {
	resp := <-bus.responses
	bus.responseCount.Add(-1)
	return resp
}

// Takes a program and returns a VM that's ready to execute the program from
// the beginning
func NewVirtualMachine(program Program) *VM {
	vm := &VM{
		stdin:       bufio.NewReader(os.Stdin),
		responseBus: newDeviceResponseBus(),
	}

	vm.pubRegisters = vm.registers[:numRegisters]
	vm.pc = &vm.pubRegisters[0]
	vm.sp = &vm.pubRegisters[1]
	vm.mode = &vm.registers[numRegisters]

	// Set process start address
	*vm.pc = reservedBytes

	// Set stack pointer to be 1 after the last valid stack address
	// (indexing this will trigger a seg fault)
	*vm.sp = heapSizeBytes

	// Set available segment to initially point to entire memory region
	vm.activeSegment = vm.memory[:]

	// Set up devices
	vm.devices[0] = newSystemTimer(DeviceBaseInfo{InterruptAddr: 0, ResponseBus: vm.responseBus})
	vm.devices[1] = newPowerController(DeviceBaseInfo{InterruptAddr: 1 * varchBytes, ResponseBus: vm.responseBus}, vm)
	vm.devices[2] = newMemoryManagement(DeviceBaseInfo{InterruptAddr: 2 * varchBytes, ResponseBus: vm.responseBus}, vm)

	// Initialize remainder of device slots with nodevice marker
	for i := 0; i < int(maxHWDevices); i++ {
		if vm.devices[i] == nil {
			vm.devices[i] = newNoDevice()
		}
	}

	if program.debugSymMap != nil {
		vm.debugOut = &strings.Builder{}
		vm.debugSym = &debugSymbols{source: program.debugSymMap}
		vm.stdout = bufio.NewWriter(vm.debugOut)
	} else {
		vm.stdout = bufio.NewWriter(os.Stdout)
	}

	for i, instr := range program.instructions {
		// Address in VM memory we will place this instruction
		baseAddr := instructionBytes*uint32(i) + reservedBytes
		bytes := vm.memory[baseAddr:]

		// Convert instruction to a series of bytes in memory
		uint16ToBytes(instr.code, bytes)
		uint16ToBytes(instr.register, bytes[2:])
		uint32ToBytes(instr.arg, bytes[4:])
	}

	vm.processInstructionBytes = uint32(len(program.instructions)) * instructionBytes
	vm.pushInitialArgumentsToStack()

	return vm
}

// Returns a tuple of (code, register, oparg) without packaging it into an Instruction type
func decodeInstruction(bytes []byte) (uint16, uint16, uint32) {
	codeRegister := uint32FromBytes(bytes)
	return uint16(codeRegister & 0x0000ffff), uint16((codeRegister & 0xffff0000) >> 16), uint32FromBytes(bytes[4:])
}

// Takes a series of bytes encoded as little endian and converts them to an instruction
// Returns an Instruction type
func decodeInstructionTyped(bytes []byte) Instruction {
	codeRegister := uint32FromBytes(bytes)
	return Instruction{
		code:     uint16(codeRegister & 0x0000ffff),
		register: uint16((codeRegister & 0xffff0000) >> 16),
		arg:      uint32FromBytes(bytes[4:]),
	}
}

// Takes an absolute stack pointer (pointing to address in global memory segment) and
// converts it to a stack pointer relative to the current stack slice
func (vm *VM) computeRelativeStackPointer(sp uint32) uint32 {
	return sp - vm.stackOffsetBytes
}

// Takes an instruction and attempts to format it in 2 ways:
//  1. if debug symbols available, use that to print original source
//  2. if no debug symbols, approximate the code (labels will have been replaced with numbers)
func formatInstructionStr(vm *VM, pc register, prefix string) string {
	if pc < heapSizeBytes {
		if vm.debugSym != nil {
			// Use debug symbols to print source as it was when first read in
			return fmt.Sprintf(prefix+" %d: %s", pc, vm.debugSym.source[int(pc)])
		} else {
			// Use instruction -> string conversion since we don't have debug symbols
			return fmt.Sprintf(prefix+" %d: %s", pc, decodeInstructionTyped(vm.memory[pc:]))
		}
	}

	return ""
}

func (vm *VM) printCurrentState() {
	instr := formatInstructionStr(vm, *vm.pc, "  next instruction>")
	if instr != "" {
		fmt.Println(instr)
	}

	fmt.Println("  registers>", vm.pubRegisters)
	fmt.Println("  stack>", vm.activeSegment[vm.computeRelativeStackPointer(*vm.sp):])

	vm.printDebugOutput()
}

func (vm *VM) printDebugOutput() {
	if vm.debugOut != nil {
		fmt.Println("  output>", revertEscapeSeqReplacements(vm.debugOut.String()))
	}
}

func (vm *VM) printProgram() {
	numInstructions := vm.processInstructionBytes / instructionBytes
	for i := range numInstructions {
		fmt.Println(formatInstructionStr(vm, register(i)*instructionBytes+reservedBytes, " "))
	}
}

// Converts uint16 to a sequence of 2 bytes encoded as little endian
func uint16ToBytes(u uint16, bytes []byte) {
	binary.LittleEndian.PutUint16(bytes, u)
}

// Converts bytes -> uint32, assuming the given bytes are at least
// a sequence of 4 and that they were encoded as little endian
func uint32FromBytes(bytes []byte) uint32 {
	return binary.LittleEndian.Uint32(bytes)
}

// Converts uint32 to a sequence of 4 bytes encoded as little endian
func uint32ToBytes(u uint32, bytes []byte) {
	binary.LittleEndian.PutUint32(bytes, u)
}

func float32ToBytes(f float32, bytes []byte) {
	uint32ToBytes(math.Float32bits(f), bytes)
}

// Returns current top of stack without moving stack pointer
func (vm *VM) peekStack() []byte {
	return vm.activeSegment[vm.computeRelativeStackPointer(*vm.sp):]
}

// Reserves space on the stack without returning anything
func (vm *VM) pushStackFast(bytes uint32) {
	*vm.sp -= bytes
}

// Removes bytes from the stack without returning anything
func (vm *VM) popStackFast(bytes uint32) {
	*vm.sp += bytes
}

// Returns top of stack before moving stack pointer forward
func (vm *VM) popStack() []byte {
	bytes := vm.activeSegment[vm.computeRelativeStackPointer(*vm.sp):]
	*vm.sp += varchBytes
	return bytes
}

// Returns top of stack (as uint32) before moving stack pointer forward
func (vm *VM) popStackUint32() uint32 {
	val := uint32FromBytes(vm.activeSegment[vm.computeRelativeStackPointer(*vm.sp):])
	*vm.sp += varchBytes
	return val
}

// Returns 1st and 2nd top stack values before moving stack pointer forward
func (vm *VM) popStackx2() ([]byte, []byte) {
	bytes := vm.activeSegment[vm.computeRelativeStackPointer(*vm.sp):]
	*vm.sp += varchBytesx2
	return bytes, bytes[varchBytes:]
}

// Returns 1st and 2nd top stack values (as uint32) before moving stack pointer forward
func (vm *VM) popStackx2Uint32() (uint32, uint32) {
	bytes := vm.activeSegment[vm.computeRelativeStackPointer(*vm.sp):]
	*vm.sp += varchBytesx2
	return uint32FromBytes(bytes), uint32FromBytes(bytes[varchBytes:])
}

// Returns the top 3 stack values (as uint32) and moves the stack pointer forward
func (vm *VM) popStackx3Uint32() (uint32, uint32, uint32) {
	bytes := vm.activeSegment[vm.computeRelativeStackPointer(*vm.sp):]
	*vm.sp += varchBytesx3
	return uint32FromBytes(bytes), uint32FromBytes(bytes[varchBytes:]), uint32FromBytes(bytes[varchBytesx2:])
}

// Pops the first argument, peeks the second
func (vm *VM) popPeekStack() ([]byte, []byte) {
	bytes := vm.activeSegment[vm.computeRelativeStackPointer(*vm.sp):]
	*vm.sp += varchBytes
	return bytes, bytes[varchBytes:]
}

// Narrows value to 1 byte and pushes it to the stack
func (vm *VM) pushStackByte(value register) {
	*vm.sp--
	vm.activeSegment[vm.computeRelativeStackPointer(*vm.sp)] = byte(value)
}

// Pushes value to stack unmodified
func (vm *VM) pushStack(value register) {
	*vm.sp -= varchBytes
	uint32ToBytes(value, vm.activeSegment[vm.computeRelativeStackPointer(*vm.sp):])
}

// Pushes a sequence of bytes to the stack
func (vm *VM) pushStackSegment(data []byte) {
	*vm.sp -= register(len(data))
	bytes := vm.activeSegment[vm.computeRelativeStackPointer(*vm.sp):]
	for i := register(0); i < uint32(len(data)); i++ {
		bytes[i] = data[i]
	}
}

// Peeks the first item off the stack, converts it to uint32 and returns the stack
// bytes that are safe to write to
func getStackOneInput(vm *VM) (uint32, []byte) {
	x := vm.peekStack()
	return uint32FromBytes(x), x
}

// Pops the first item off the stack, peeks the second
// Converts both inputs to uint32 and returns the stack bytes that are safe to write to
func getStackTwoInputs(vm *VM) (uint32, uint32, []byte) {
	x, y := vm.popPeekStack()
	return uint32FromBytes(x), uint32FromBytes(y), y
}

func compare[T numeric32](x, y T) uint32 {
	if x < y {
		return math.MaxUint32 // -1 when converted to int32
	} else if x > y {
		return 1
	} else {
		return 0
	}
}

func arithRemi[T integer32](x, y T) (uint32, error) {
	if y == 0 {
		return 0, errDivisionByZero
	}

	return uint32(x % y), nil
}

// Instruction fetch, decode+execute
//
// This is considered a tight loop. Some of the normal programming conveniences and patterns
// don't work well here since we need to be able to execute this as many times per second
// as possible (hundreds of millions+ times per second). Even the overhead of a true function call (non-inlined) is too much.
//
// It's ok to move certain things to functions if the instructions are very simple (meaning Go's inlining rules take over),
// but otherwise it's best to try and embed the logic directly into the switch statement.
//
// singleStep can be set when in debug mode so that this function runs 1 instruction
// and then returns to caller.
//
// The current design of this function attempts to balance performance, readability and code reuse.
func (vm *VM) execInstructions(singleStep bool) {
	for {
		// Possible this was set to non-nil during poweroff
		if vm.errcode != nil {
			return
		}

		pc := vm.pc
		// if *pc >= heapSizeBytes {
		// 	vm.errcode = errProcessFinished
		// 	return
		// }

		if vm.responseBus.Ready() {
			resp := vm.responseBus.Receive()
			handlerAddr := uint32FromBytes(vm.memory[resp.interruptAddr:])

			if handlerAddr != 0 {
				sp := *vm.sp

				// Store state related to current frame first
				vm.pushStack(*pc)
				vm.pushStack(sp)
				vm.pushStack(*vm.mode)

				// Store response information next
				vm.pushStackSegment(resp.data)
				vm.pushStack(uint32(len(resp.data)))
				vm.pushStack(resp.id)

				// Redirect program counter to the handler's address
				*pc = handlerAddr
			}
		}

		code, opreg, oparg := decodeInstruction(vm.memory[*pc:])
		*pc += instructionBytes

		switch code {
		case nopNoArgs:
		case byteOneArg:
			vm.pushStackByte(oparg)
		case constOneArg:
			vm.pushStack(oparg)
		case loadOneArg:
			vm.pushStack(vm.pubRegisters[opreg])
		case storeOneArg:
			regVal := uint32FromBytes(vm.popStack())
			vm.pubRegisters[opreg] = register(regVal)
		case kstoreOneArg:
			regVal := uint32FromBytes(vm.peekStack())
			vm.pubRegisters[opreg] = register(regVal)
		case loadp8NoArgs:
			bytes := vm.peekStack()
			addr := vm.computeRelativeStackPointer(uint32FromBytes(bytes))
			uint32ToBytes(uint32(vm.activeSegment[addr]), bytes)
		case loadp16NoArgs:
			bytes := vm.peekStack()
			addr := vm.computeRelativeStackPointer(uint32FromBytes(bytes))
			uint32ToBytes(uint32(binary.LittleEndian.Uint16(vm.activeSegment[addr:])), bytes)
		case loadp32NoArgs:
			bytes := vm.peekStack()
			addr := vm.computeRelativeStackPointer(uint32FromBytes(bytes))
			uint32ToBytes(uint32(binary.LittleEndian.Uint32(vm.activeSegment[addr:])), bytes)
		case storep8NoArgs:
			addrBytes, valueBytes := vm.popStackx2()
			addr := vm.computeRelativeStackPointer(uint32FromBytes(addrBytes))
			vm.activeSegment[addr] = valueBytes[0]
		case storep16NoArgs:
			addrBytes, valueBytes := vm.popStackx2()
			addr := vm.computeRelativeStackPointer(uint32FromBytes(addrBytes))

			// unrolled loop
			vm.activeSegment[addr] = valueBytes[0]
			vm.activeSegment[addr+1] = valueBytes[1]
		case storep32NoArgs:
			addrBytes, valueBytes := vm.popStackx2()
			addr := vm.computeRelativeStackPointer(uint32FromBytes(addrBytes))

			// unrolled loop
			vm.activeSegment[addr] = valueBytes[0]
			vm.activeSegment[addr+1] = valueBytes[1]
			vm.activeSegment[addr+2] = valueBytes[2]
			vm.activeSegment[addr+3] = valueBytes[3]
		case pushNoArgs:
			// push with no args, meaning we pull # bytes from the stack
			bytes := vm.popStackUint32()
			vm.pushStackFast(bytes)
			// This will ensure we catch invalid stack addresses
			var _ = vm.activeSegment[vm.computeRelativeStackPointer(*vm.sp)]
		case pushOneArg:
			// push <constant> meaning the byte value is inlined
			vm.pushStackFast(oparg)
			// This will ensure we catch invalid stack addresses
			var _ = vm.activeSegment[vm.computeRelativeStackPointer(*vm.sp)]
		case popNoArgs:
			bytes := vm.popStackUint32()
			vm.popStackFast(bytes)
			// This will ensure we catch invalid stack addresses
			var _ = vm.activeSegment[vm.computeRelativeStackPointer(*vm.sp)]
		case popOneArg:
			vm.popStackFast(oparg)
			// This will ensure we catch invalid stack addresses
			var _ = vm.activeSegment[vm.computeRelativeStackPointer(*vm.sp)]

		// Begin add instructions
		case addiNoArgs:
			x, y, bytes := getStackTwoInputs(vm)
			uint32ToBytes(x+y, bytes)
		case addiOneArg:
			x, bytes := getStackOneInput(vm)
			uint32ToBytes(x+oparg, bytes)
		case addfNoArgs:
			x, y, bytes := getStackTwoInputs(vm)
			float32ToBytes(math.Float32frombits(x)+math.Float32frombits(y), bytes)
		case addfOneArg:
			x, bytes := getStackOneInput(vm)
			float32ToBytes(math.Float32frombits(x)+math.Float32frombits(oparg), bytes)

		// Begin sub instructions
		case subiNoArgs:
			x, y, bytes := getStackTwoInputs(vm)
			uint32ToBytes(x-y, bytes)
		case subiOneArg:
			x, bytes := getStackOneInput(vm)
			uint32ToBytes(x-oparg, bytes)
		case subfNoArgs:
			x, y, bytes := getStackTwoInputs(vm)
			float32ToBytes(math.Float32frombits(x)-math.Float32frombits(y), bytes)
		case subfOneArg:
			x, bytes := getStackOneInput(vm)
			float32ToBytes(math.Float32frombits(x)-math.Float32frombits(oparg), bytes)

		// Begin mul instructions
		case muliNoArgs:
			x, y, bytes := getStackTwoInputs(vm)
			uint32ToBytes(x*y, bytes)
		case muliOneArg:
			x, bytes := getStackOneInput(vm)
			uint32ToBytes(x*oparg, bytes)
		case mulfNoArgs:
			x, y, bytes := getStackTwoInputs(vm)
			float32ToBytes(math.Float32frombits(x)*math.Float32frombits(y), bytes)
		case mulfOneArg:
			x, bytes := getStackOneInput(vm)
			float32ToBytes(math.Float32frombits(x)*math.Float32frombits(oparg), bytes)

		// Begin div instructions
		case diviNoArgs:
			x, y, bytes := getStackTwoInputs(vm)
			// For ints we need to check for div by 0
			// See https://stackoverflow.com/questions/23505212/floating-point-is-an-equality-comparison-enough-to-prevent-division-by-zero
			// and its discussion
			if y == 0 {
				vm.errcode = errDivisionByZero
				return
			}

			uint32ToBytes(x/y, bytes)
		case diviOneArg:
			// For ints we need to check for div by 0
			// See https://stackoverflow.com/questions/23505212/floating-point-is-an-equality-comparison-enough-to-prevent-division-by-zero
			// and its discussion
			if oparg == 0 {
				vm.errcode = errDivisionByZero
				return
			}

			x, bytes := getStackOneInput(vm)
			uint32ToBytes(x/oparg, bytes)
		case divfNoArgs:
			x, y, bytes := getStackTwoInputs(vm)
			float32ToBytes(math.Float32frombits(x)/math.Float32frombits(y), bytes)
		case divfOneArg:
			x, bytes := getStackOneInput(vm)
			float32ToBytes(math.Float32frombits(x)/math.Float32frombits(oparg), bytes)

		// Begin radd instructions
		case raddiOneArg:
			x, bytes := getStackOneInput(vm)
			vm.pubRegisters[opreg] += x
			uint32ToBytes(vm.pubRegisters[opreg], bytes)
		case raddiTwoArgs:
			vm.pubRegisters[opreg] += oparg
			vm.pushStack(vm.pubRegisters[opreg])
		case raddfOneArg:
			x, bytes := getStackOneInput(vm)
			vm.pubRegisters[opreg] = math.Float32bits(math.Float32frombits(vm.pubRegisters[opreg]) + math.Float32frombits(x))
			uint32ToBytes(vm.pubRegisters[opreg], bytes)
		case raddfTwoArgs:
			vm.pubRegisters[opreg] = math.Float32bits(math.Float32frombits(vm.pubRegisters[opreg]) + math.Float32frombits(oparg))
			vm.pushStack(vm.pubRegisters[opreg])

		// Begin rsub instructions
		case rsubiOneArg:
			x, bytes := getStackOneInput(vm)
			vm.pubRegisters[opreg] -= x
			uint32ToBytes(vm.pubRegisters[opreg], bytes)
		case rsubiTwoArgs:
			vm.pubRegisters[opreg] -= oparg
			vm.pushStack(vm.pubRegisters[opreg])
		case rsubfOneArg:
			x, bytes := getStackOneInput(vm)
			vm.pubRegisters[opreg] = math.Float32bits(math.Float32frombits(vm.pubRegisters[opreg]) - math.Float32frombits(x))
			uint32ToBytes(vm.pubRegisters[opreg], bytes)
		case rsubfTwoArgs:
			vm.pubRegisters[opreg] = math.Float32bits(math.Float32frombits(vm.pubRegisters[opreg]) - math.Float32frombits(oparg))
			vm.pushStack(vm.pubRegisters[opreg])

		// Begin rmul instructions
		case rmuliOneArg:
			x, bytes := getStackOneInput(vm)
			vm.pubRegisters[opreg] *= x
			uint32ToBytes(vm.pubRegisters[opreg], bytes)
		case rmuliTwoArgs:
			vm.pubRegisters[opreg] *= oparg
			vm.pushStack(vm.pubRegisters[opreg])
		case rmulfOneArg:
			x, bytes := getStackOneInput(vm)
			vm.pubRegisters[opreg] = math.Float32bits(math.Float32frombits(vm.pubRegisters[opreg]) * math.Float32frombits(x))
			uint32ToBytes(vm.pubRegisters[opreg], bytes)
		case rmulfTwoArgs:
			vm.pubRegisters[opreg] = math.Float32bits(math.Float32frombits(vm.pubRegisters[opreg]) * math.Float32frombits(oparg))
			vm.pushStack(vm.pubRegisters[opreg])

		// Begin rdiv instructions
		case rdiviOneArg:
			x, bytes := getStackOneInput(vm)
			// For ints we need to check for div by 0
			// See https://stackoverflow.com/questions/23505212/floating-point-is-an-equality-comparison-enough-to-prevent-division-by-zero
			// and its discussion
			if x == 0 {
				vm.errcode = errDivisionByZero
				return
			}

			vm.pubRegisters[opreg] /= x
			uint32ToBytes(vm.pubRegisters[opreg], bytes)
		case rdiviTwoArgs:
			// For ints we need to check for div by 0
			// See https://stackoverflow.com/questions/23505212/floating-point-is-an-equality-comparison-enough-to-prevent-division-by-zero
			// and its discussion
			if oparg == 0 {
				vm.errcode = errDivisionByZero
				return
			}

			vm.pubRegisters[opreg] /= oparg
			vm.pushStack(vm.pubRegisters[opreg])
		case rdivfOneArg:
			x, bytes := getStackOneInput(vm)
			vm.pubRegisters[opreg] = math.Float32bits(math.Float32frombits(vm.pubRegisters[opreg]) / math.Float32frombits(x))
			uint32ToBytes(vm.pubRegisters[opreg], bytes)
		case rdivfTwoArgs:
			vm.pubRegisters[opreg] = math.Float32bits(math.Float32frombits(vm.pubRegisters[opreg]) / math.Float32frombits(oparg))
			vm.pushStack(vm.pubRegisters[opreg])

		// Begin register shift instructions
		case rshiftLOneArg:
			x, bytes := getStackOneInput(vm)
			vm.pubRegisters[opreg] <<= x
			uint32ToBytes(vm.pubRegisters[opreg], bytes)
		case rshiftLTwoArgs:
			vm.pubRegisters[opreg] <<= oparg
			vm.pushStack(vm.pubRegisters[opreg])

		case rshiftROneArg:
			x, bytes := getStackOneInput(vm)
			vm.pubRegisters[opreg] >>= x
			uint32ToBytes(vm.pubRegisters[opreg], bytes)

		case rshiftRTwoargs:
			vm.pubRegisters[opreg] >>= oparg
			vm.pushStack(vm.pubRegisters[opreg])

		// Begin remainder instructions
		case remuNoArgs:
			var resultVal uint32
			x, y, bytes := getStackTwoInputs(vm)
			resultVal, vm.errcode = arithRemi(x, y)
			uint32ToBytes(resultVal, bytes)
		case remuOneArg:
			var resultVal uint32
			x, bytes := getStackOneInput(vm)
			resultVal, vm.errcode = arithRemi(x, oparg)
			uint32ToBytes(resultVal, bytes)

		case remsNoArgs:
			var resultVal uint32
			x, y, bytes := getStackTwoInputs(vm)
			resultVal, vm.errcode = arithRemi(int32(x), int32(y))
			uint32ToBytes(resultVal, bytes)
		case remsOneArg:
			var resultVal uint32
			x, bytes := getStackOneInput(vm)
			resultVal, vm.errcode = arithRemi(int32(x), int32(oparg))
			uint32ToBytes(resultVal, bytes)

		case remfNoArgs:
			x, y, bytes := getStackTwoInputs(vm)
			// Go's math.Mod returns remainder after floating point division
			rem := math.Mod(float64(math.Float32frombits(x)), float64(math.Float32frombits(y)))
			uint32ToBytes(math.Float32bits(float32(rem)), bytes)
		case remfOneArg:
			x, bytes := getStackOneInput(vm)
			// Go's math.Mod returns remainder after floating point division
			rem := math.Mod(float64(math.Float32frombits(x)), float64(math.Float32frombits(oparg)))
			uint32ToBytes(math.Float32bits(float32(rem)), bytes)

		// Begin logic instructions
		case notNoArgs:
			x, bytes := getStackOneInput(vm)
			// Invert all bits, store result on stack
			uint32ToBytes(^x, bytes)

		case andNoArgs:
			x, y, bytes := getStackTwoInputs(vm)
			uint32ToBytes(x&y, bytes)
		case andOneArg:
			x, bytes := getStackOneInput(vm)
			uint32ToBytes(x&oparg, bytes)

		case orNoArgs:
			x, y, bytes := getStackTwoInputs(vm)
			uint32ToBytes(x|y, bytes)
		case orOneArg:
			x, bytes := getStackOneInput(vm)
			uint32ToBytes(x|oparg, bytes)

		case xorNoArgs:
			x, y, bytes := getStackTwoInputs(vm)
			uint32ToBytes(x^y, bytes)
		case xorOneArg:
			x, bytes := getStackOneInput(vm)
			uint32ToBytes(x^oparg, bytes)

		// Begin left/right shift instructions
		case shiftLNoArgs:
			x, y, bytes := getStackTwoInputs(vm)
			uint32ToBytes(x<<y, bytes)
		case shiftLOneArg:
			x, bytes := getStackOneInput(vm)
			uint32ToBytes(x<<oparg, bytes)

		case shiftRNoArgs:
			x, y, bytes := getStackTwoInputs(vm)
			uint32ToBytes(x>>y, bytes)
		case shiftROneArg:
			x, bytes := getStackOneInput(vm)
			uint32ToBytes(x>>oparg, bytes)

		// Begin jump instructions
		case jmpNoArgs:
			*pc = register(uint32FromBytes(vm.popStack()))
		case jmpOneArg:
			*pc = register(oparg)

		case jzNoArgs:
			addr, value := vm.popStackx2Uint32()
			if value == 0 {
				*pc = addr
			}
		case jzOneArg:
			addr, value := oparg, vm.popStackUint32()
			if value == 0 {
				*pc = addr
			}

		case jnzNoArgs:
			addr, value := vm.popStackx2Uint32()
			if value != 0 {
				*pc = addr
			}
		case jnzOneArg:
			addr, value := oparg, vm.popStackUint32()
			if value != 0 {
				*pc = addr
			}

		case jleNoArgs:
			addr, value := vm.popStackx2Uint32()
			if int32(value) <= 0 {
				*pc = addr
			}
		case jleOneArg:
			addr, value := oparg, vm.popStackUint32()
			if int32(value) <= 0 {
				*pc = addr
			}

		case jlNoArgs:
			addr, value := vm.popStackx2Uint32()
			if int32(value) < 0 {
				*pc = addr
			}
		case jlOneArg:
			addr, value := oparg, vm.popStackUint32()
			if int32(value) < 0 {
				*pc = addr
			}

		case jgeNoArgs:
			addr, value := vm.popStackx2Uint32()
			if int32(value) >= 0 {
				*pc = addr
			}
		case jgeOneArg:
			addr, value := oparg, vm.popStackUint32()
			if int32(value) >= 0 {
				*pc = addr
			}

		case jgNoArgs:
			addr, value := vm.popStackx2Uint32()
			if int32(value) > 0 {
				*pc = addr
			}
		case jgOneArg:
			addr, value := oparg, vm.popStackUint32()
			if int32(value) > 0 {
				*pc = addr
			}

		// Begin comparison instructions
		case cmpuNoArgs:
			x, y, bytes := getStackTwoInputs(vm)
			uint32ToBytes(compare(x, y), bytes)
		case cmpsNoArgs:
			x, y, bytes := getStackTwoInputs(vm)
			uint32ToBytes(compare(int32(x), int32(y)), bytes)
		case cmpfNoArgs:
			x, y, bytes := getStackTwoInputs(vm)
			uint32ToBytes(compare(math.Float32frombits(x), math.Float32frombits(y)), bytes)

		// Begin console IO instructions
		// case writebNoArgs:
		// 	addr := vm.computeRelativeStackPointer(vm.popStackUint32())
		// 	vm.stdout.WriteByte(vm.activeSegment[addr])
		// case writecNoArgs:
		// 	character := rune(vm.popStackUint32())
		// 	vm.stdout.WriteRune(character)
		// case flushNoArgs:
		// 	vm.stdout.Flush()
		// case readcNoArgs:
		// 	character, _, err := vm.stdin.ReadRune()
		// 	if err != nil {
		// 		vm.errcode = errIO
		// 		return
		// 	}
		// 	vm.pushStack(uint32(character))

		case srLoadOneArg:
			// privilege check
			if *vm.mode != 0 {
				vm.errcode = errIllegelInstruction
				return
			}

			vm.pushStack(vm.registers[opreg])
		case srStoreOneArg:
			// privilege check
			if *vm.mode != 0 {
				vm.errcode = errIllegelInstruction
				return
			}

			regVal := uint32FromBytes(vm.popStack())
			vm.registers[opreg] = register(regVal)

			// Allow memory management device to potentially update memory bounds (if store
			// register was vm.mode)
			vm.devices[2].TrySend(0, 3, nil)

		case sysintOneArg:
			if oparg < restrictedInterruptsAddrRange {
				// Perform privilege check to make sure calling code can actually initiate a
				// privileged interrupt
				if *vm.mode != 0 {
					vm.errcode = errIllegelInstruction
					return
				}
			}

			// Push caller frame info to the stack so we can resume later
			vm.pushStack(*pc)
			vm.pushStack(*vm.sp)
			vm.pushStack(*vm.mode)

			// Update the program counter to be the interrupt handler's address
			*pc = uint32FromBytes(vm.memory[oparg:])

		case resumeNoArgs:
			// privilege check
			if *vm.mode != 0 {
				vm.errcode = errIllegelInstruction
				return
			}

			prevMode, prevSp, prevPc := vm.popStackx3Uint32()
			// Since resume is a privileged instruction, we know the current mode must be 0
			// If prevMode is anything other than 0 (unprivileged), update the mode and notify memory manager
			if prevMode != 0 {
				*vm.mode = prevMode
				// Allow memory management device to potentially update memory bounds
				vm.devices[2].TrySend(0, 3, nil)
			}

			*vm.sp = prevSp
			*vm.pc = prevPc

		case writeTwoArgs:
			// privilege check
			if *vm.mode != 0 {
				vm.errcode = errIllegelInstruction
				return
			}

			if oparg == 0 {
				hwinfo := vm.devices[opreg].GetInfo()
				vm.pushStackSegment(hwinfo.Metadata)
				vm.pushStack(uint32(len(hwinfo.Metadata)))
				vm.pushStack(hwinfo.HWID)
			} else {
				interactionId, numBytes := vm.popStackx2Uint32()
				sptr := *vm.sp
				data := vm.activeSegment[sptr : sptr+numBytes]

				vm.popStackFast(numBytes)
				vm.pushStack(vm.devices[opreg].TrySend(interactionId, oparg, data))
			}

		case haltNoArgs:
			// privilege check
			if *vm.mode != 0 {
				vm.errcode = errIllegelInstruction
				return
			}

			// Sets the pc to be this instruction (continues loop until interrupt)
			*pc -= instructionBytes

		default:
			// Shouldn't get here since we preprocess+parse all source into
			// valid instructions before executing
			vm.errcode = errUnknownInstruction
			return
		}

		if singleStep {
			return
		}
	}
}
