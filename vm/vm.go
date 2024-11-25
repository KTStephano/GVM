package gvm

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
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

type VM struct {
	registers [numRegisters]register
	pc        *register // program counter
	sp        *register // stack pointer

	stack   [stackSize]byte // grows down (largest address towards smallest address)
	program []Instruction

	// Allows vm to read/write to some type of output
	stdout *bufio.Writer
	stdin  *bufio.Reader

	// This gets written to whenever program encounters a normal or critical error
	errcode error

	// Debug flags
	debugOut *strings.Builder
	debugSym *debugSymbols
}

type integer32 interface {
	int32 | uint32
}

// Constrains to types we can freely interpret their 32 bit pattern
type numeric32 interface {
	integer32 | float32
}

const (
	numRegisters uint32 = 32
	stackSize    uint32 = 65536
	// 4 bytes since our virtual architecture is 32-bit
	varchBytes   register = 4
	varchBytesx2 register = 2 * varchBytes
)

var (
	errProgramFinished    = errors.New("ran out of instructions")
	errSegmentationFault  = errors.New("segmentation fault")
	errDivisionByZero     = errors.New("division by zero")
	errUnknownInstruction = errors.New("instruction not recognized")
	errIO                 = errors.New("input-output error")
)

// Takes a program and returns a VM that's ready to execute the program from
// the beginning
func NewVirtualMachine(program Program) *VM {
	vm := &VM{
		program: program.instructions,
		stdin:   bufio.NewReader(os.Stdin),
	}

	vm.pc = &vm.registers[0]
	vm.sp = &vm.registers[1]
	// Set stack pointer to be 1 after the last valid stack address
	// (indexing this will trigger a seg fault)
	*vm.sp = stackSize

	if program.debugSymMap != nil {
		vm.debugOut = &strings.Builder{}
		vm.debugSym = &debugSymbols{source: program.debugSymMap}
		vm.stdout = bufio.NewWriter(vm.debugOut)
	} else {
		vm.stdout = bufio.NewWriter(os.Stdout)
	}

	return vm
}

// Takes an instruction and attempts to format it in 2 ways:
//  1. if debug symbols available, use that to print original source
//  2. if no debug symbols, approximate the code (labels will have been replaced with numbers)
func formatInstructionStr(vm *VM, pc register, prefix string) string {
	if pc < register(len(vm.program)) {
		if vm.debugSym != nil {
			// Use debug symbols to print source as it was when first read in
			return fmt.Sprintf(prefix+" %d: %s", pc, vm.debugSym.source[int(pc)])
		} else {
			// Use instruction -> string conversion since we don't have debug symbols
			return fmt.Sprintf(prefix+" %d: %s", pc, vm.program[pc])
		}
	}

	return ""
}

func (vm *VM) printCurrentState() {
	instr := formatInstructionStr(vm, *vm.pc, "  next instruction>")
	if instr != "" {
		fmt.Println(instr)
	}

	fmt.Println("  registers>", vm.registers)
	fmt.Println("  stack>", vm.stack[*vm.sp:])

	vm.printDebugOutput()
}

func (vm *VM) printDebugOutput() {
	if vm.debugOut != nil {
		fmt.Println("  output>", revertEscapeSeqReplacements(vm.debugOut.String()))
	}
}

func (vm *VM) printProgram() {
	for i := range vm.program {
		fmt.Println(formatInstructionStr(vm, register(i), " "))
	}
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
	return vm.stack[*vm.sp:]
}

// Returns top of stack before moving stack pointer forward
func (vm *VM) popStack() []byte {
	bytes := vm.stack[*vm.sp:]
	*vm.sp += varchBytes
	return bytes
}

// Returns top of stack (as uint32) before moving stack pointer forward
func (vm *VM) popStackUint32() uint32 {
	val := uint32FromBytes(vm.stack[*vm.sp:])
	*vm.sp += varchBytes
	return val
}

// Returns 1st and 2nd top stack values before moving stack pointer forward
func (vm *VM) popStackx2() ([]byte, []byte) {
	bytes := vm.stack[*vm.sp:]
	*vm.sp += varchBytesx2
	return bytes, bytes[varchBytes:]
}

// Returns 1st and 2nd top stack values (as uint32) before moving stack pointer forward
func (vm *VM) popStackx2Uint32() (uint32, uint32) {
	bytes := vm.stack[*vm.sp:]
	*vm.sp += varchBytesx2
	return uint32FromBytes(bytes), uint32FromBytes(bytes[varchBytes:])
}

// Pops the first argument, peeks the second
func (vm *VM) popPeekStack() ([]byte, []byte) {
	bytes := vm.stack[*vm.sp:]
	*vm.sp += varchBytes
	return bytes, bytes[varchBytes:]
}

// Narrows value to 1 byte and pushes it to the stack
func (vm *VM) pushStackByte(value register) {
	*vm.sp--
	vm.stack[*vm.sp] = byte(value)
}

// Pushes value to stack unmodified
func (vm *VM) pushStack(value register) {
	*vm.sp -= varchBytes
	uint32ToBytes(value, vm.stack[*vm.sp:])
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
		pc := vm.pc
		if *pc >= register(len(vm.program)) {
			vm.errcode = errProgramFinished
			return
		}

		instr := vm.program[*vm.pc]
		code := instr.code
		opreg := instr.register
		oparg := instr.arg

		*pc++

		switch code {
		case nopNoArgs:
		case byteOneArg:
			vm.pushStackByte(oparg)
		case constOneArg:
			vm.pushStack(oparg)
		case loadOneArg:
			vm.pushStack(vm.registers[opreg])
		case storeOneArg:
			regVal := uint32FromBytes(vm.popStack())
			vm.registers[opreg] = register(regVal)
		case kstoreOneArg:
			regVal := uint32FromBytes(vm.peekStack())
			vm.registers[opreg] = register(regVal)
		case loadp8NoArgs:
			bytes := vm.peekStack()
			addr := uint32FromBytes(bytes)
			uint32ToBytes(uint32(vm.stack[addr]), bytes)
		case loadp16NoArgs:
			bytes := vm.peekStack()
			addr := uint32FromBytes(bytes)
			uint32ToBytes(uint32(binary.LittleEndian.Uint16(vm.stack[addr:])), bytes)
		case loadp32NoArgs:
			bytes := vm.peekStack()
			addr := uint32FromBytes(bytes)
			uint32ToBytes(uint32(binary.LittleEndian.Uint32(vm.stack[addr:])), bytes)
		case storep8NoArgs:
			addrBytes, valueBytes := vm.popStackx2()
			addr := uint32FromBytes(addrBytes)
			vm.stack[addr] = valueBytes[0]
		case storep16NoArgs:
			addrBytes, valueBytes := vm.popStackx2()
			addr := uint32FromBytes(addrBytes)

			// unrolled loop
			vm.stack[addr] = valueBytes[0]
			vm.stack[addr+1] = valueBytes[1]
		case storep32NoArgs:
			addrBytes, valueBytes := vm.popStackx2()
			addr := uint32FromBytes(addrBytes)

			// unrolled loop
			vm.stack[addr] = valueBytes[0]
			vm.stack[addr+1] = valueBytes[1]
			vm.stack[addr+2] = valueBytes[2]
			vm.stack[addr+3] = valueBytes[3]
		case pushNoArgs:
			// push with no args, meaning we pull # bytes from the stack
			bytes := vm.popStackUint32()
			*vm.sp -= register(bytes)
			// This will ensure we catch invalid stack addresses
			var _ = vm.stack[*vm.sp]
		case pushOneArg:
			// push <constant> meaning the byte value is inlined
			*vm.sp -= register(oparg)
			// This will ensure we catch invalid stack addresses
			var _ = vm.stack[*vm.sp]
		case popNoArgs:
			bytes := vm.popStackUint32()
			*vm.sp = *vm.sp + register(bytes)
			// This will ensure we catch invalid stack addresses
			var _ = vm.stack[*vm.sp]
		case popOneArg:
			*vm.sp -= register(oparg)
			// This will ensure we catch invalid stack addresses
			var _ = vm.stack[*vm.sp]

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
			vm.registers[opreg] += x
			uint32ToBytes(vm.registers[opreg], bytes)
		case raddiTwoArgs:
			vm.registers[opreg] += oparg
			vm.pushStack(vm.registers[opreg])
		case raddfOneArg:
			x, bytes := getStackOneInput(vm)
			vm.registers[opreg] = math.Float32bits(math.Float32frombits(vm.registers[opreg]) + math.Float32frombits(x))
			uint32ToBytes(vm.registers[opreg], bytes)
		case raddfTwoArgs:
			vm.registers[opreg] = math.Float32bits(math.Float32frombits(vm.registers[opreg]) + math.Float32frombits(oparg))
			vm.pushStack(vm.registers[opreg])

		// Begin rsub instructions
		case rsubiOneArg:
			x, bytes := getStackOneInput(vm)
			vm.registers[opreg] -= x
			uint32ToBytes(vm.registers[opreg], bytes)
		case rsubiTwoArgs:
			vm.registers[opreg] -= oparg
			vm.pushStack(vm.registers[opreg])
		case rsubfOneArg:
			x, bytes := getStackOneInput(vm)
			vm.registers[opreg] = math.Float32bits(math.Float32frombits(vm.registers[opreg]) - math.Float32frombits(x))
			uint32ToBytes(vm.registers[opreg], bytes)
		case rsubfTwoArgs:
			vm.registers[opreg] = math.Float32bits(math.Float32frombits(vm.registers[opreg]) - math.Float32frombits(oparg))
			vm.pushStack(vm.registers[opreg])

		// Begin rmul instructions
		case rmuliOneArg:
			x, bytes := getStackOneInput(vm)
			vm.registers[opreg] *= x
			uint32ToBytes(vm.registers[opreg], bytes)
		case rmuliTwoArgs:
			vm.registers[opreg] *= oparg
			vm.pushStack(vm.registers[opreg])
		case rmulfOneArg:
			x, bytes := getStackOneInput(vm)
			vm.registers[opreg] = math.Float32bits(math.Float32frombits(vm.registers[opreg]) * math.Float32frombits(x))
			uint32ToBytes(vm.registers[opreg], bytes)
		case rmulfTwoArgs:
			vm.registers[opreg] = math.Float32bits(math.Float32frombits(vm.registers[opreg]) * math.Float32frombits(oparg))
			vm.pushStack(vm.registers[opreg])

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

			vm.registers[opreg] /= x
			uint32ToBytes(vm.registers[opreg], bytes)
		case rdiviTwoArgs:
			// For ints we need to check for div by 0
			// See https://stackoverflow.com/questions/23505212/floating-point-is-an-equality-comparison-enough-to-prevent-division-by-zero
			// and its discussion
			if oparg == 0 {
				vm.errcode = errDivisionByZero
				return
			}

			vm.registers[opreg] /= oparg
			vm.pushStack(vm.registers[opreg])
		case rdivfOneArg:
			x, bytes := getStackOneInput(vm)
			vm.registers[opreg] = math.Float32bits(math.Float32frombits(vm.registers[opreg]) / math.Float32frombits(x))
			uint32ToBytes(vm.registers[opreg], bytes)
		case rdivfTwoArgs:
			vm.registers[opreg] = math.Float32bits(math.Float32frombits(vm.registers[opreg]) / math.Float32frombits(oparg))
			vm.pushStack(vm.registers[opreg])

		// Begin register shift instructions
		case rshiftLOneArg:
			x, bytes := getStackOneInput(vm)
			vm.registers[opreg] <<= x
			uint32ToBytes(vm.registers[opreg], bytes)
		case rshiftLTwoArgs:
			vm.registers[opreg] <<= oparg
			vm.pushStack(vm.registers[opreg])

		case rshiftROneArg:
			x, bytes := getStackOneInput(vm)
			vm.registers[opreg] >>= x
			uint32ToBytes(vm.registers[opreg], bytes)

		case rshiftRTwoargs:
			vm.registers[opreg] >>= oparg
			vm.pushStack(vm.registers[opreg])

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
		case writebNoArgs:
			addr := vm.popStackUint32()
			vm.stdout.WriteByte(vm.stack[addr])
		case writecNoArgs:
			character := rune(vm.popStackUint32())
			vm.stdout.WriteRune(character)
		case flushNoArgs:
			vm.stdout.Flush()
		case readcNoArgs:
			character, _, err := vm.stdin.ReadRune()
			if err != nil {
				vm.errcode = errIO
				return
			}
			vm.pushStack(uint32(character))

		case exitNoArgs:
			// Sets the pc to be one after the last instruction
			*pc = register(len(vm.program))

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
