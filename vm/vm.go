package gvm

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
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
			nop   (no operation)
			byte  (pushes byte value onto the stack)
			const (pushes const value onto stack (can be a label))
			load  (loads value of register)
			store (stores value of stack[0] to register)
			loadp8, loadp16, loadp32 (loads 8, 16 or 32 bit value from address at stack[0], widens to 32 bits)
				loadpX are essentially stack[0] = *stack[0]
			storep8, storep16, storep32 (narrows stack[1] to 8, 16 or 32 bits and writes it to address at stack[0])
				storepX are essentially *stack[0] = stack[1]

		The push/pop instructions accept an optional argument. This argument is the number of bytes to push to or pop from the stack.
		If no argument is specified, stack[0] should hold the bytes argument.

			push (reserve bytes on the stack, advances stack pointer)
			pop  (free bytes back to the stack, retracts stack pointer)

		addi, addf, muli and mulf all accept an optional argument. This is a fast path that will perform stack[0] <op> arg. The reason
		the same isn't present for sub or div is because they're not commutative.

			addi, addf (int and float add)
			subi, subf (int and float sub)
			muli, mulf (int and float mul)
			divi, divf (int and float div)

		and, or, xor all take an optional argument. This is a fast path that will perform stack[0] <op> arg and then overwrite
		the stack value with the result.

			not (inverts all bits of stack[0])
			and (logical AND between stack[0] and stack[1])
			or  (logical OR between stack[0] and stack[1])
			xor (logical XOR between stack[0] and stack[1])

		Each of the jump instructions accept an optional argument. If no argument is specified, stack[0] is where
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

			writec (writes 1 32-bit value to stdout from stack[0])
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

	stack   [stackSize]byte
	program []Instruction

	// Allows vm to read/write to some type of output
	stdout *bufio.Writer
	stdin  *bufio.Reader

	// This gets written to whenever program encounters a normal or critical error
	errcode error

	// Debug data
	debugOut *strings.Builder
	debugSym *debugSymbols
}

// Constrains to types we can freely interpret their 32 bit pattern
type numeric32 interface {
	int32 | uint32 | float32
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
	errIllegalOperation   = errors.New("illegal operation at instruction")
	errUnknownInstruction = errors.New("instruction not recognized")
	errIO                 = errors.New("input-output error")
)

func NewVirtualMachine(debug bool, files ...string) (*VM, error) {
	vm := &VM{stdin: bufio.NewReader(os.Stdin)}
	vm.pc = &vm.registers[0]
	vm.sp = &vm.registers[1]
	// Set stack pointer to be 1 after the last valid stack address
	// (indexing this will trigger a seg fault)
	*vm.sp = stackSize

	// If requested, set up the VM in debug mode
	var debugSymMap map[int]string
	if debug {
		debugSymMap = make(map[int]string)
		vm.debugOut = &strings.Builder{}
		vm.debugSym = &debugSymbols{source: debugSymMap}
		vm.stdout = bufio.NewWriter(vm.debugOut)
	} else {
		vm.stdout = bufio.NewWriter(os.Stdout)
	}

	// Read each file
	lines := make([]string, 0)
	for _, filename := range files {
		file, err := os.Open(filename)
		if err != nil {
			fmt.Println("Could not read", filename)
			return nil, err
		}

		reader := bufio.NewReader(file)
		for {
			line, _, err := reader.ReadLine()
			if err != nil {
				break
			}

			lines = append(lines, string(line))
		}
	}

	// Maps from regex(label) -> address string
	labels := make(map[*regexp.Regexp]string)
	preprocessedLines := make([][2]string, 0)

	// First preprocess line to remove whitespace lines and convert labels
	// into line numbers
	for _, line := range lines {
		var err error
		preprocessedLines, err = preprocessLine(string(line), labels, preprocessedLines, debugSymMap)
		if err != nil {
			return nil, err
		}
	}

	vm.program = make([]Instruction, 0, len(preprocessedLines))

	for _, line := range preprocessedLines {
		// Replace all labels with their instruction address
		for label, lineNum := range labels {
			line[1] = label.ReplaceAllString(line[1], lineNum)
		}

		instrs, err := parseInputLine(line)
		if err != nil {
			return nil, err
		}

		vm.program = append(vm.program, instrs)
	}

	return vm, nil
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
	} else if x == y {
		return 0
	} else {
		return 1
	}
}

func arithAddi(x, y []byte) {
	// Overwrite y with result
	uint32ToBytes(uint32FromBytes(x)+uint32FromBytes(y), y)
}

func arithAddf(x, y []byte) {
	// Overwrite y with result
	float32ToBytes(float32FromBytes(x)+float32FromBytes(y), y)
}

func arithAddiFast(vm *VM, y uint32) {
	x := vm.peekStack()
	uint32ToBytes(uint32FromBytes(x)+y, x)
}

func arithAddfFast(vm *VM, y uint32) {
	x := vm.peekStack()
	float32ToBytes(float32FromBytes(x)+math.Float32frombits(y), x)
}

func arithSubi(x, y []byte) {
	// Overwrite y with result
	uint32ToBytes(uint32FromBytes(x)-uint32FromBytes(y), y)
}

func arithSubf(x, y []byte) {
	// Overwrite y with result
	float32ToBytes(float32FromBytes(x)-float32FromBytes(y), y)
}

func arithMuli(x, y []byte) {
	// Overwrite y with result
	uint32ToBytes(uint32FromBytes(x)*uint32FromBytes(y), y)
}

func arithMulf(x, y []byte) {
	// Overwrite y with result
	float32ToBytes(float32FromBytes(x)*float32FromBytes(y), y)
}

func arithMuliFast(vm *VM, y uint32) {
	x := vm.peekStack()
	uint32ToBytes(uint32FromBytes(x)*y, x)
}

func arithMulfFast(vm *VM, y uint32) {
	x := vm.peekStack()
	float32ToBytes(float32FromBytes(x)*math.Float32frombits(y), x)
}

func arithDivi(x, y []byte) {
	// Overwrite y with result
	uint32ToBytes(uint32FromBytes(x)/uint32FromBytes(y), y)
}

func arithDivf(x, y []byte) {
	// Overwrite y with result
	float32ToBytes(float32FromBytes(x)/float32FromBytes(y), y)
}

func logicalAnd(x, y []byte) {
	// Overwrite y with result
	uint32ToBytes(uint32FromBytes(x)&uint32FromBytes(y), y)
}

func logicalAndFast(vm *VM, y uint32) {
	x := vm.peekStack()
	uint32ToBytes(uint32FromBytes(x)&y, x)
}

func logicalOr(x, y []byte) {
	// Overwrite y with result
	uint32ToBytes(uint32FromBytes(x)|uint32FromBytes(y), y)
}

func logicalOrFast(vm *VM, y uint32) {
	x := vm.peekStack()
	uint32ToBytes(uint32FromBytes(x)|y, x)
}

func logicalXor(x, y []byte) {
	// Overwrite y with result
	uint32ToBytes(uint32FromBytes(x)^uint32FromBytes(y), y)
}

func logicalXorFast(vm *VM, y uint32) {
	x := vm.peekStack()
	uint32ToBytes(uint32FromBytes(x)^y, x)
}

// Returns value (bytes) for the push/pop instructions. If data > 0
// it will use oparg as the value, otherwise it will pop the bytes value
// from the stack
func getPushPopValue(vm *VM, oparg, data uint32) uint32 {
	if data == 0 {
		return vm.popStackUint32()
	} else {
		return oparg
	}
}

// Returns (addr, value) for the conditional jumps. If data is > 0
// it will use oparg as the address, otherwise it will pop the address
// from the stack.
func getJumpAddrValue(vm *VM, oparg, data uint32) (uint32, uint32) {
	if data == 0 {
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

		instr := vm.program[*pc]
		code, data := instr.DecodeInstruction()
		oparg := instr.arg
		*pc++

		switch code {
		case Nop:
		case Byte:
			vm.pushStackByte(oparg)
		case Const:
			vm.pushStack(oparg)
		case Load:
			vm.pushStack(vm.registers[oparg])
		case Store:
			if oparg < 2 {
				// not allowed to write to program counter or stack pointer
				vm.errcode = errIllegalOperation
				return
			}

			regValue := uint32FromBytes(vm.popStack())
			vm.registers[oparg] = register(regValue)
		case Loadp8:
			addrBytes := vm.peekStack()
			addr := uint32FromBytes(addrBytes)
			// overwrite addrBytes with memory value
			uint32ToBytes(uint32(vm.stack[addr]), addrBytes)
		case Loadp16:
			addrBytes := vm.peekStack()
			addr := uint32FromBytes(addrBytes)
			// overwrite addrBytes with memory value
			uint32ToBytes(uint32(binary.LittleEndian.Uint16(vm.stack[addr:])), addrBytes)
		case Loadp32:
			addrBytes := vm.peekStack()
			addr := uint32FromBytes(addrBytes)
			// overwrite addrBytes with memory value
			uint32ToBytes(uint32(binary.LittleEndian.Uint32(vm.stack[addr:])), addrBytes)
		case Storep8:
			addrBytes, valueBytes := vm.popStackx2()
			addr := uint32FromBytes(addrBytes)
			vm.stack[addr] = valueBytes[0]
		case Storep16:
			addrBytes, valueBytes := vm.popStackx2()
			addr := uint32FromBytes(addrBytes)

			// unrolled loop
			vm.stack[addr] = valueBytes[0]
			vm.stack[addr+1] = valueBytes[1]
		case Storep32:
			addrBytes, valueBytes := vm.popStackx2()
			addr := uint32FromBytes(addrBytes)

			// unrolled loop
			vm.stack[addr] = valueBytes[0]
			vm.stack[addr+1] = valueBytes[1]
			vm.stack[addr+2] = valueBytes[2]
			vm.stack[addr+3] = valueBytes[3]
		case Push:
			bytes := getPushPopValue(vm, oparg, data)
			*vm.sp = *vm.sp - register(bytes)
			// This will ensure we catch invalid stack addresses
			var _ = vm.stack[*vm.sp]
		case Pop:
			bytes := getPushPopValue(vm, oparg, data)
			*vm.sp = *vm.sp + register(bytes)
			// This will ensure we catch invalid stack addresses
			var _ = vm.stack[*vm.sp]
		case Addi:
			if data == 0 {
				arg0Bytes, arg1Bytes := vm.popPeekStack()
				// Overwrites arg1Bytes with result of op
				arithAddi(arg0Bytes, arg1Bytes)
			} else {
				arithAddiFast(vm, oparg)
			}
		case Addf:
			if data == 0 {
				arg0Bytes, arg1Bytes := vm.popPeekStack()
				// Overwrites arg1Bytes with result of op
				arithAddf(arg0Bytes, arg1Bytes)
			} else {
				arithAddfFast(vm, oparg)
			}
		case Subi:
			arg0Bytes, arg1Bytes := vm.popPeekStack()
			// Overwrites arg1Bytes with result of op
			arithSubi(arg0Bytes, arg1Bytes)
		case Subf:
			arg0Bytes, arg1Bytes := vm.popPeekStack()
			// Overwrites arg1Bytes with result of op
			arithSubf(arg0Bytes, arg1Bytes)
		case Muli:
			if data == 0 {
				arg0Bytes, arg1Bytes := vm.popPeekStack()
				// Overwrites arg1Bytes with result of op
				arithMuli(arg0Bytes, arg1Bytes)
			} else {
				arithMuliFast(vm, oparg)
			}
		case Mulf:
			if data == 0 {
				arg0Bytes, arg1Bytes := vm.popPeekStack()
				// Overwrites arg1Bytes with result of op
				arithMulf(arg0Bytes, arg1Bytes)
			} else {
				arithMulfFast(vm, oparg)
			}
		case Divi:
			arg0Bytes, arg1Bytes := vm.popPeekStack()
			// Overwrites arg1Bytes with result of op
			arithDivi(arg0Bytes, arg1Bytes)
		case Divf:
			arg0Bytes, arg1Bytes := vm.popPeekStack()
			// Overwrites arg1Bytes with result of op
			arithDivf(arg0Bytes, arg1Bytes)
		case Not:
			arg := vm.peekStack()
			// Invert all bits, store result in arg
			uint32ToBytes(^uint32FromBytes(arg), arg)
		case And:
			if data == 0 {
				arg0Bytes, arg1Bytes := vm.popPeekStack()
				// Overwrites arg1Bytes with result of op
				logicalAnd(arg0Bytes, arg1Bytes)
			} else {
				logicalAndFast(vm, oparg)
			}
		case Or:
			if data == 0 {
				arg0Bytes, arg1Bytes := vm.popPeekStack()
				// Overwrites arg1Bytes with result of op
				logicalOr(arg0Bytes, arg1Bytes)
			} else {
				logicalOrFast(vm, oparg)
			}
		case Xor:
			if data == 0 {
				arg0Bytes, arg1Bytes := vm.popPeekStack()
				// Overwrites arg1Bytes with result of op
				logicalXor(arg0Bytes, arg1Bytes)
			} else {
				logicalXorFast(vm, oparg)
			}
		case Jmp:
			addr := oparg
			if data == 0 {
				addr = uint32FromBytes(vm.popStack())
			}
			*pc = register(addr)
		case Jz:
			addr, value := getJumpAddrValue(vm, oparg, data)
			if value == 0 {
				*vm.pc = addr
			}
		case Jnz:
			addr, value := getJumpAddrValue(vm, oparg, data)
			if value != 0 {
				*vm.pc = addr
			}
		case Jle:
			addr, value := getJumpAddrValue(vm, oparg, data)
			if int32(value) <= 0 {
				*vm.pc = addr
			}
		case Jl:
			addr, value := getJumpAddrValue(vm, oparg, data)
			if int32(value) < 0 {
				*vm.pc = addr
			}
		case Jge:
			addr, value := getJumpAddrValue(vm, oparg, data)
			if int32(value) >= 0 {
				*vm.pc = addr
			}
		case Jg:
			addr, value := getJumpAddrValue(vm, oparg, data)
			if int32(value) > 0 {
				*vm.pc = addr
			}
		case Cmpu:
			x, y := vm.popPeekStack()
			// Overwrite y bytes with result of compare
			uint32ToBytes(compare(uint32FromBytes(x), uint32FromBytes(y)), y)
		case Cmps:
			x, y := vm.popPeekStack()
			// Overwrite y bytes with result of compare
			uint32ToBytes(compare(int32FromBytes(x), int32FromBytes(y)), y)
		case Cmpf:
			x, y := vm.popPeekStack()
			// Overwrite y bytes with result of compare
			uint32ToBytes(compare(float32FromBytes(x), float32FromBytes(y)), y)
		case Writec:
			character := rune(uint32FromBytes(vm.popStack()))
			vm.stdout.WriteString(string(character))
			vm.stdout.Flush()
		case Readc:
			character, _, err := vm.stdin.ReadRune()
			if err != nil {
				vm.errcode = errIO
				return
			}
			vm.pushStack(uint32(character))
		case Exit:
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
