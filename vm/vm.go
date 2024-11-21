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
			storep8, storep16, storep32 (narrows stack[1] to 8, 16 or 32 bits and writes it to address at stack[0])

			push (reserve bytes on the stack, advances stack pointer)
			pop  (free bytes back to the stack, retracts stack pointer)

			addi, addf (int and float add)
			subi, subf (int and float sub)
			muli, mulf (int and float mul)
			divi, divf (int and float div)

			not (inverts all bits of stack[0])
			and (logical AND between stack[0] and stack[1])
			or  (logical OR between stack[0] and stack[1])
			xor (logical XOR between stack[0] and stack[1])

		The jump instructions all pop the address off the stack (stack[0]), but the comparison versions
		only read the value at stack[1] without popping it

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

// We store these if we want extra debug information
type debugSymbols struct {
	// maps from line num -> source
	source map[int]string
}

type VM struct {
	registers [numRegisters]Register
	pc        *Register // program counter
	sp        *Register // stack pointer

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
	numRegisters int = 32
	stackSize    int = 65536
	// 4 bytes since our virtual architecture is 32-bit
	varchBytes   Register = 4
	varchBytesx2 Register = 2 * varchBytes
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

	labels := make(map[string]string)
	preprocessedLines := make([][2]string, 0)

	// Allows us to easily find and replace commands from start to end of line
	comments := regexp.MustCompile("//.*")

	// First preprocess line to remove whitespace lines and convert labels
	// into line numbers
	for _, line := range lines {
		var err error
		preprocessedLines, err = preprocessLine(string(line), comments, labels, preprocessedLines, debugSymMap)
		if err != nil {
			return nil, err
		}
	}

	vm.program = make([]Instruction, 0, len(preprocessedLines))

	for _, line := range preprocessedLines {
		// Replace all labels with their instruction address
		for label, lineNum := range labels {
			line[1] = strings.ReplaceAll(line[1], label, lineNum)
		}

		instrs, err := parseInputLine(line)
		if err != nil {
			return nil, err
		}

		vm.program = append(vm.program, instrs...)
	}

	return vm, nil
}

func (vm *VM) formatInstructionStr(prefix string) string {
	pc := vm.pc
	if *pc < Register(len(vm.program)) {
		fmtStr := prefix + " %d: %s"
		if vm.debugSym != nil {
			return fmt.Sprintf(fmtStr, *pc, vm.debugSym.source[int(*pc)])
		} else {
			return fmt.Sprintf(fmtStr, *pc, vm.program[*pc])
		}
	}

	return ""
}

func (vm *VM) printCurrentState() {
	instr := vm.formatInstructionStr("next instruction>")
	if instr != "" {
		fmt.Println(instr)
	}

	fmt.Println("registers>", vm.registers)
	// Prints the stack in reverse order, meaning the first element is actually the last
	// that will be removed
	fmt.Println("reverse stack>", vm.stack[:*vm.sp])

	vm.printDebugOutput()
}

func (vm *VM) printDebugOutput() {
	if vm.debugOut != nil {
		fmt.Println("output>", revertEscapeSeqReplacements(vm.debugOut.String()))
	}
}

func (vm *VM) printProgram() {
	for i, instr := range vm.program {
		if vm.debugSym != nil {
			fmt.Printf("%d: %s\n", i, vm.debugSym.source[i])
		} else {
			fmt.Printf("%d: %s\n", i, instr)
		}
	}
}

func uint32FromBytes(bytes []byte) uint32 {
	return binary.LittleEndian.Uint32(bytes)
}

func int32FromBytes(bytes []byte) int32 {
	return int32(uint32FromBytes(bytes))
}

func float32FromBytes(bytes []byte) float32 {
	return math.Float32frombits(uint32FromBytes(bytes))
}

func uint32ToBytes(u uint32, bytes []byte) {
	binary.LittleEndian.PutUint32(bytes, u)
}

func float32ToBytes(f float32, bytes []byte) {
	uint32ToBytes(math.Float32bits(f), bytes)
}

func (vm *VM) peekStack(offset Register) []byte {
	return vm.stack[*vm.sp-offset:]
}

func (vm *VM) popStack() []byte {
	start := *vm.sp - varchBytes
	*vm.sp = start
	return vm.stack[start:]
}

func (vm *VM) popStackx2() ([]byte, []byte) {
	*vm.sp -= varchBytesx2
	bytes := vm.stack[*vm.sp:]
	return bytes[4:], bytes
}

// Pops the first element, peeks the second element
func (vm *VM) popPeekStack() ([]byte, []byte) {
	*vm.sp -= varchBytes
	bytes := vm.stack[*vm.sp-varchBytes:]
	return bytes[4:], bytes
}

// Pops the first element, peeks the second element
// Converts both to uint32
func (vm *VM) popPeekStackUint32() (uint32, uint32) {
	*vm.sp -= varchBytes
	bytes := vm.stack[*vm.sp-varchBytes:]
	return uint32FromBytes(bytes[4:]), uint32FromBytes(bytes)
}

func (vm *VM) pushStackByte(value Register) {
	start := *vm.sp
	*vm.sp++
	vm.stack[start] = byte(value)
}

func (vm *VM) pushStack(value Register) {
	start := *vm.sp
	*vm.sp += varchBytes
	uint32ToBytes(value, vm.stack[start:])
}

func compare[T numeric32](vm *VM, convertFunc func([]byte) T) {
	arg0Bytes, arg1Bytes := vm.popPeekStack()

	a0T := convertFunc(arg0Bytes)
	a1T := convertFunc(arg1Bytes)
	var result uint32
	if a0T < a1T {
		result = math.MaxUint32 // -1 when converted to int32
	} else if a0T == a1T {
		result = 0
	} else {
		result = 1
	}

	// Overwrite arg1 bytes with result of compare
	uint32ToBytes(result, arg1Bytes)
}

func arithAddi(x, y []byte) {
	// Overwrite y with result
	uint32ToBytes(uint32FromBytes(x)+uint32FromBytes(y), y)
}

func arithAddf(x, y []byte) {
	// Overwrite y with result
	float32ToBytes(float32FromBytes(x)+float32FromBytes(y), y)
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

func logicalOr(x, y []byte) {
	// Overwrite y with result
	uint32ToBytes(uint32FromBytes(x)|uint32FromBytes(y), y)
}

func logicalXor(x, y []byte) {
	// Overwrite y with result
	uint32ToBytes(uint32FromBytes(x)^uint32FromBytes(y), y)
}

// This is considered a tight loop. It's ok to move certain things to functions
// if the functions are very simple (meaning Go's inlining rules take over), but
// otherwise it's best to try and embed the logic directly into the switch statement.
func (vm *VM) execInstructions(singleStep bool) {
	for {
		pc := vm.pc
		if *pc >= Register(len(vm.program)) {
			vm.errcode = errProgramFinished
			return
		}

		instr := vm.program[*pc]
		*pc += 1

		switch instr.code {
		case Nop:
		case Byte:
			vm.pushStackByte(instr.arg)
		case Const:
			vm.pushStack(instr.arg)
		case Load:
			vm.pushStack(vm.registers[instr.arg])
		case Store:
			regIdx := instr.arg
			if regIdx < 2 {
				// not allowed to write to program counter or stack pointer
				vm.errcode = errIllegalOperation
				return
			}

			regValue := uint32FromBytes(vm.popStack())
			vm.registers[regIdx] = Register(regValue)
		case Loadp8:
			addrBytes := vm.peekStack(varchBytes)
			addr := uint32FromBytes(addrBytes)
			// overwrite addrBytes with memory value
			uint32ToBytes(uint32(vm.stack[addr]), addrBytes)
		case Loadp16:
			addrBytes := vm.peekStack(varchBytes)
			addr := uint32FromBytes(addrBytes)
			// overwrite addrBytes with memory value
			uint32ToBytes(uint32(binary.LittleEndian.Uint16(vm.stack[addr:])), addrBytes)
		case Loadp32:
			addrBytes := vm.peekStack(varchBytes)
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
			bytes := uint32FromBytes(vm.popStack())
			*vm.sp = *vm.sp + Register(bytes)
		case Pop:
			bytes := uint32FromBytes(vm.popStack())
			*vm.sp = *vm.sp - Register(bytes)
		case Addi:
			arg0Bytes, arg1Bytes := vm.popPeekStack()
			// Overwrites arg1Bytes with result of op
			arithAddi(arg0Bytes, arg1Bytes)
		case Addf:
			arg0Bytes, arg1Bytes := vm.popPeekStack()
			// Overwrites arg1Bytes with result of op
			arithAddf(arg0Bytes, arg1Bytes)
		case Subi:
			arg0Bytes, arg1Bytes := vm.popPeekStack()
			// Overwrites arg1Bytes with result of op
			arithSubi(arg0Bytes, arg1Bytes)
		case Subf:
			arg0Bytes, arg1Bytes := vm.popPeekStack()
			// Overwrites arg1Bytes with result of op
			arithSubf(arg0Bytes, arg1Bytes)
		case Muli:
			arg0Bytes, arg1Bytes := vm.popPeekStack()
			// Overwrites arg1Bytes with result of op
			arithMuli(arg0Bytes, arg1Bytes)
		case Mulf:
			arg0Bytes, arg1Bytes := vm.popPeekStack()
			// Overwrites arg1Bytes with result of op
			arithMulf(arg0Bytes, arg1Bytes)
		case Divi:
			arg0Bytes, arg1Bytes := vm.popPeekStack()
			// Overwrites arg1Bytes with result of op
			arithDivi(arg0Bytes, arg1Bytes)
		case Divf:
			arg0Bytes, arg1Bytes := vm.popPeekStack()
			// Overwrites arg1Bytes with result of op
			arithDivf(arg0Bytes, arg1Bytes)
		case Not:
			arg := vm.peekStack(varchBytes)
			// Invert all bits, store result in arg
			uint32ToBytes(^uint32FromBytes(arg), arg)
		case And:
			arg0Bytes, arg1Bytes := vm.popPeekStack()
			// Overwrites arg1Bytes with result of op
			logicalAnd(arg0Bytes, arg1Bytes)
		case Or:
			arg0Bytes, arg1Bytes := vm.popPeekStack()
			// Overwrites arg1Bytes with result of op
			logicalOr(arg0Bytes, arg1Bytes)
		case Xor:
			arg0Bytes, arg1Bytes := vm.popPeekStack()
			// Overwrites arg1Bytes with result of op
			logicalXor(arg0Bytes, arg1Bytes)
		case Jmp:
			addr := uint32FromBytes(vm.popStack())
			*pc = Register(addr)
		case Jz:
			addr, value := vm.popPeekStackUint32()
			if value == 0 {
				*vm.pc = addr
			}
		case Jnz:
			addr, value := vm.popPeekStackUint32()
			if value != 0 {
				*vm.pc = addr
			}
		case Jle:
			addr, value := vm.popPeekStackUint32()
			if int32(value) <= 0 {
				*vm.pc = addr
			}
		case Jl:
			addr, value := vm.popPeekStackUint32()
			if int32(value) < 0 {
				*vm.pc = addr
			}
		case Jge:
			addr, value := vm.popPeekStackUint32()
			if int32(value) >= 0 {
				*vm.pc = addr
			}
		case Jg:
			addr, value := vm.popPeekStackUint32()
			if int32(value) > 0 {
				*vm.pc = addr
			}
		case Cmpu:
			compare(vm, uint32FromBytes)
		case Cmps:
			compare(vm, int32FromBytes)
		case Cmpf:
			compare(vm, float32FromBytes)
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
			*pc = Register(len(vm.program))
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
