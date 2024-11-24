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

/*
	For each CPU core:
			- little endian
			- 32-bit virtual architecture
			- 32 registers starting at index 0
			- register 0 is the program counter
			- register 1 is the stack pointer
			- registers indexed 2 through 31 are general purpose, 32-bit
			- supports single stepping through instructions
			- supports setting program breakpoints

	The stack is 64kb in size minimum

	Possible bytecodes
			nop    (no operation)
			byte   (pushes byte value onto the stack)
			const  (pushes const value onto stack (can be a label))
			load   (loads value of register)
			store  (stores value of stack[0] to register)
			kstore (stores value of stack[0] to register and keeps value on the stack)
			loadp8, loadp16, loadp32 (loads 8, 16 or 32 bit value from address at stack[0], widens to 32 bits)
				loadpX are essentially stack[0] = *stack[0]
			storep8, storep16, storep32 (narrows stack[1] to 8, 16 or 32 bits and writes it to address at stack[0])
				storepX are essentially *stack[0] = stack[1]

		The push/pop instructions accept an numArgs argument. This argument is the number of bytes to push to or pop from the stack.
		If no argument is specified, stack[0] should hold the bytes argument.

			push (reserve bytes on the stack, advances stack pointer)
			pop  (free bytes back to the stack, retracts stack pointer)

		All arithmetic instructions accept an numArgs argument. This is a fast path that will perform stack[0] <op> arg and overwrite
		the current stack value with the result.

			addi, addf (int and float add)
			subi, subf (int and float sub)
			muli, mulf (int and float mul)
			divi, divf (int and float div)

		The remainder functions work the same as % in languages such as C. It returns the remainder after dividing stack[0] and stack[1].
		There is a fast path for these as well that performs remainder stack[0] arg.

			remu, rems (unsigned and signed remainder after integer division)
			remf	   (remainder after floating point division)

		and, or, xor instructions all take an numArgs argument. This is a fast path that will perform stack[0] <op> arg and then overwrite
		the current stack value with the result.

			not (inverts all bits of stack[0])
			and (logical AND between stack[0] and stack[1])
			or  (logical OR between stack[0] and stack[1])
			xor (logical XOR between stack[0] and stack[1])

		Each of the jump instructions accept an numArgs argument. If no argument is specified, stack[0] is where
		they check for their jump address. Otherwise the argument is treated as the jump address.

		Example: jnz addr (jump to addr if stack[0] is not 0)
				 jnz	  (jump to stack[0] if stack[1] is not 0)

			jmp  (unconditional jump to address at stack[0])
			jz   (jump to address at stack[0] if stack[1] is 0)
			jnz  (jump to address at stack[0] if stack[1] is not 0)
			jle  (jump to address at stack[0] if stack[1] less than or equal to 0)
			jl   (jump to address at stack[0] if stack[1] less than 0)
			jge  (jump to address at stack[0] if stack[1] greater than or equal to 0)
			jg   (jump to address at stack[0] if stack[1] greater than 0)

		The following all do: (compare stack[0] to stack[1]: negative if stack[0] < stack[1], 0 if stack[0] == stack[1], positive if stack[0] > stack[1])
		However, the naming scheme is as follows:
				cmpu -> treats both inputs as unsigned 32-bit
				cmps -> treats both inputs as signed 32-bit
				cmpf -> treats both inputs as float 32-bit

			cmpu
			cmps
			cmpf

			writeb (writes 1 8-bit value to stdout buffer from address stored at stack[0])
			writec (writes 1 32-bit value to stdout buffer from stack[0])
			flush  (flushes stdout buffer to console)
			readc  (reads 1 character from stdin - pushes to stack as 32-bit value)

			exit (stops the program)

	Examples:
			const 3 // stack: [3]
			const 5 // stack: [5, 3]
			addi    // stack: [8]

			const 2 // stack: [2, 8]
			store	// stack: [],      register 2: 8

			const 2 // stack: [2],     register 2: 8
			load	// stack: [8],     register 2: 8
			const 4 // stack: [4, 8],  register 2: 8
			addi 	// stack: [12],	   register 2: 8

			const 2 // stack: [2, 12], register 2: 8
			store	// stack: [], 	   register 2: 12
*/

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

func int32FromBytes(bytes []byte) int32 {
	return int32(uint32FromBytes(bytes))
}

func float32FromBytes(bytes []byte) float32 {
	return math.Float32frombits(uint32FromBytes(bytes))
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

func getArithLogicValsPeek(vm *VM, oparg uint32, flags uint16) (uint32, uint32, []byte) {
	if flags == 0 {
		x, y := vm.popPeekStack()
		return uint32FromBytes(x), uint32FromBytes(y), y
	} else {
		x := vm.peekStack()
		return uint32FromBytes(x), oparg, x
	}
}

func getArithLogicValsPop(vm *VM, oparg uint32, flags uint16) (uint32, uint32) {
	if flags == 0 {
		return vm.popStackx2Uint32()
	} else {
		return vm.popStackUint32(), oparg
	}
}

func arithRemi[T integer32](x, y T) (uint32, error) {
	if y == 0 {
		return 0, errDivisionByZero
	}

	return uint32(x % y), nil
}

// Returns value (bytes) for the push/pop instructions. If flags > 0
// it will use oparg as the value, otherwise it will pop the bytes value
// from the stack
func getPushPopValue(vm *VM, oparg uint32, numArgs uint16) uint32 {
	if numArgs == 0 {
		return vm.popStackUint32()
	} else {
		return oparg
	}
}

// Returns (addr, value) for the conditional jumps. If flags is > 0
// it will use oparg as the address, otherwise it will pop the address
// from the stack.
func getJumpAddrValue(vm *VM, oparg uint32, numArgs uint16) (uint32, uint32) {
	if numArgs == 0 {
		return vm.popStackx2Uint32()
	} else {
		return oparg, vm.popStackUint32()
	}
}

// This is considered a tight loop. It's ok to move certain things to functions
// if the functions are very simple (meaning Go's inlining rules take over), but
// otherwise it's best to try and embed the logic directly into the switch statement.
//
// singleStep can be set when in debug mode so that this function runs 1 instruction
// and then returns to caller.
//
// If an instruction requires arguments, they will be laid out as 32-bit instruction values
// next to the main instruction in memory
func (vm *VM) execInstructions(singleStep bool) {
	for {
		pc := vm.pc
		if *pc >= register(len(vm.program)) {
			vm.errcode = errProgramFinished
			return
		}

		instr := vm.program[*vm.pc]
		code, numArgs := instr.code, instr.flags
		oparg := instr.arg

		*pc++

		switch code {
		case (Nop):
		case (Byte):
			vm.pushStackByte(oparg)
		case (Const):
			vm.pushStack(oparg)
		case (Load):
			vm.pushStack(vm.registers[oparg])
		case (Store):
			regVal := uint32FromBytes(vm.popStack())
			vm.registers[oparg] = register(regVal)
		case (Kstore):
			regVal := uint32FromBytes(vm.peekStack())
			vm.registers[oparg] = register(regVal)
		case (Loadp8):
			bytes := vm.peekStack()
			addr := uint32FromBytes(bytes)
			uint32ToBytes(uint32(vm.stack[addr]), bytes)
		case (Loadp16):
			bytes := vm.peekStack()
			addr := uint32FromBytes(bytes)
			uint32ToBytes(uint32(binary.LittleEndian.Uint16(vm.stack[addr:])), bytes)
		case (Loadp32):
			bytes := vm.peekStack()
			addr := uint32FromBytes(bytes)
			uint32ToBytes(uint32(binary.LittleEndian.Uint32(vm.stack[addr:])), bytes)
		case (Storep8):
			addrBytes, valueBytes := vm.popStackx2()
			addr := uint32FromBytes(addrBytes)
			vm.stack[addr] = valueBytes[0]
		case (Storep16):
			addrBytes, valueBytes := vm.popStackx2()
			addr := uint32FromBytes(addrBytes)

			// unrolled loop
			vm.stack[addr] = valueBytes[0]
			vm.stack[addr+1] = valueBytes[1]
		case (Storep32):
			addrBytes, valueBytes := vm.popStackx2()
			addr := uint32FromBytes(addrBytes)

			// unrolled loop
			vm.stack[addr] = valueBytes[0]
			vm.stack[addr+1] = valueBytes[1]
			vm.stack[addr+2] = valueBytes[2]
			vm.stack[addr+3] = valueBytes[3]
		case (Push):
			bytes := getPushPopValue(vm, oparg, numArgs)
			*vm.sp = *vm.sp - register(bytes)
			// This will ensure we catch invalid stack addresses
			var _ = vm.stack[*vm.sp]
		case (Pop):
			bytes := getPushPopValue(vm, oparg, numArgs)
			*vm.sp = *vm.sp + register(bytes)
			// This will ensure we catch invalid stack addresses
			var _ = vm.stack[*vm.sp]
		case (Addi):
			x, y, bytes := getArithLogicValsPeek(vm, oparg, numArgs)
			uint32ToBytes(x+y, bytes)
		case (Addf):
			x, y, bytes := getArithLogicValsPeek(vm, oparg, numArgs)
			float32ToBytes(math.Float32frombits(x)+math.Float32frombits(y), bytes)
		case (Subi):
			x, y, bytes := getArithLogicValsPeek(vm, oparg, numArgs)
			uint32ToBytes(x-y, bytes)
		case (Subf):
			x, y, bytes := getArithLogicValsPeek(vm, oparg, numArgs)
			float32ToBytes(math.Float32frombits(x)-math.Float32frombits(y), bytes)
		case (Muli):
			x, y, bytes := getArithLogicValsPeek(vm, oparg, numArgs)
			uint32ToBytes(x*y, bytes)
		case (Mulf):
			x, y, bytes := getArithLogicValsPeek(vm, oparg, numArgs)
			float32ToBytes(math.Float32frombits(x)*math.Float32frombits(y), bytes)
		case (Divi):
			x, y, bytes := getArithLogicValsPeek(vm, oparg, numArgs)
			// For ints we need to check for div by 0
			// See https://stackoverflow.com/questions/23505212/floating-point-is-an-equality-comparison-enough-to-prevent-division-by-zero
			// and its discussion
			if y == 0 {
				vm.errcode = errDivisionByZero
				return
			}

			uint32ToBytes(x/y, bytes)
		case (Divf):
			x, y, bytes := getArithLogicValsPeek(vm, oparg, numArgs)
			float32ToBytes(math.Float32frombits(x)/math.Float32frombits(y), bytes)
		case (Remu):
			var resultVal uint32
			x, y, bytes := getArithLogicValsPeek(vm, oparg, numArgs)
			// For ints we need to check for div by 0
			// See https://stackoverflow.com/questions/23505212/floating-point-is-an-equality-comparison-enough-to-prevent-division-by-zero
			// and its discussion
			if y == 0 {
				vm.errcode = errDivisionByZero
				return
			}

			resultVal, vm.errcode = arithRemi(x, y)
			uint32ToBytes(resultVal, bytes)
		case (Rems):
			var resultVal uint32
			x, y, bytes := getArithLogicValsPeek(vm, oparg, numArgs)
			// For ints we need to check for div by 0
			// See https://stackoverflow.com/questions/23505212/floating-point-is-an-equality-comparison-enough-to-prevent-division-by-zero
			// and its discussion
			if y == 0 {
				vm.errcode = errDivisionByZero
				return
			}

			resultVal, vm.errcode = arithRemi(int32(x), int32(y))
			uint32ToBytes(resultVal, bytes)
		case (Remf):
			x, y, bytes := getArithLogicValsPeek(vm, oparg, numArgs)
			// Go's math.Mod returns remainder after floating point division
			rem := math.Mod(float64(math.Float32frombits(x)), float64(math.Float32frombits(y)))
			uint32ToBytes(math.Float32bits(float32(rem)), bytes)
		case (Not):
			bytes := vm.peekStack()
			// Invert all bits, store result in arg
			uint32ToBytes(^uint32FromBytes(bytes), bytes)
		case (And):
			x, y, bytes := getArithLogicValsPeek(vm, oparg, numArgs)
			uint32ToBytes(x&y, bytes)
		case (Or):
			x, y, bytes := getArithLogicValsPeek(vm, oparg, numArgs)
			uint32ToBytes(x|y, bytes)
		case (Xor):
			x, y, bytes := getArithLogicValsPeek(vm, oparg, numArgs)
			uint32ToBytes(x^y, bytes)
		case (Jmp):
			addr := oparg
			if numArgs == 0 {
				addr = uint32FromBytes(vm.popStack())
			}
			*pc = register(addr)
		case (Jz):
			addr, value := getJumpAddrValue(vm, oparg, numArgs)
			if value == 0 {
				*pc = addr
			}
		case (Jnz):
			addr, value := getJumpAddrValue(vm, oparg, numArgs)
			if value != 0 {
				*pc = addr
			}
		case (Jle):
			addr, value := getJumpAddrValue(vm, oparg, numArgs)
			if int32(value) <= 0 {
				*pc = addr
			}
		case (Jl):
			addr, value := getJumpAddrValue(vm, oparg, numArgs)
			if int32(value) < 0 {
				*pc = addr
			}
		case (Jge):
			addr, value := getJumpAddrValue(vm, oparg, numArgs)
			if int32(value) >= 0 {
				*pc = addr
			}
		case (Jg):
			addr, value := getJumpAddrValue(vm, oparg, numArgs)
			if int32(value) > 0 {
				*pc = addr
			}
		case (Cmpu):
			x, y := vm.popPeekStack()
			// Overwrite y bytes with result of compare
			uint32ToBytes(compare(uint32FromBytes(x), uint32FromBytes(y)), y)
		case (Cmps):
			x, y := vm.popPeekStack()
			// Overwrite y bytes with result of compare
			uint32ToBytes(compare(int32FromBytes(x), int32FromBytes(y)), y)
		case (Cmpf):
			x, y := vm.popPeekStack()
			// Overwrite y bytes with result of compare
			uint32ToBytes(compare(float32FromBytes(x), float32FromBytes(y)), y)
		case (Writeb):
			addr := uint32FromBytes(vm.popStack())
			vm.stdout.WriteByte(vm.stack[addr])
		case (Writec):
			character := rune(uint32FromBytes(vm.popStack()))
			vm.stdout.WriteRune(character)
		case (Flush):
			vm.stdout.Flush()
		case (Readc):
			character, _, err := vm.stdin.ReadRune()
			if err != nil {
				vm.errcode = errIO
				return
			}
			vm.pushStack(uint32(character))
		case (Exit):
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
