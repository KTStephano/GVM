package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unsafe"
)

/*
	For each CPU core:
		- little endian
		- 32-bit virtual architecture
		- 32 registers starting at index 0
		- register 0 is the program counter
		- register 1 is the stack pointer
		- registers indexed 2 through 31 are general purpose, 32-bit

	The stack is 64kb in size minimum

	Possible bytecodes
		nop   (no operation)
		byte  (pushes byte value onto the stack)
		const (pushes const value onto stack (can be a label))
		load  (loads value of register at index stack[0])
		store (stores value of stack[1] to register at index stack[0])
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

		jmp (unconditional jump to program address at stack[0])
		jz  (jump if stack[0] is 0)
		jnz (jump if stack[0] is not 0)
		jle (jump if stack[0] less than or equal to 0)
		jl  (jump if stack[0] less than 0)
		jge (jump if stack[0] greater than or equal to 0)
		jg  (jump if stack[0] greater than 0)

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

type Bytecode byte

const (
	Nop      Bytecode = 0x00
	Byte     Bytecode = 0x01
	Const    Bytecode = 0x02
	Load     Bytecode = 0x03
	Store    Bytecode = 0x04
	Loadp8   Bytecode = 0x05
	Loadp16  Bytecode = 0x06
	Loadp32  Bytecode = 0x07
	Storep8  Bytecode = 0x08
	Storep16 Bytecode = 0x09
	Storep32 Bytecode = 0x0A
	Push     Bytecode = 0x0B
	Pop      Bytecode = 0x0C
	Addi     Bytecode = 0x0D
	Addf     Bytecode = 0x0E
	Subi     Bytecode = 0x0F
	Subf     Bytecode = 0x10
	Muli     Bytecode = 0x11
	Mulf     Bytecode = 0x12
	Divi     Bytecode = 0x13
	Divf     Bytecode = 0x14
	Not      Bytecode = 0x15
	And      Bytecode = 0x16
	Or       Bytecode = 0x17
	Xor      Bytecode = 0x18
	Jmp      Bytecode = 0x19
	Jz       Bytecode = 0x1A
	Jnz      Bytecode = 0x1B
	Jle      Bytecode = 0x1C
	Jl       Bytecode = 0x1D
	Jge      Bytecode = 0x1E
	Jg       Bytecode = 0x1F
	Cmpu     Bytecode = 0x20
	Cmps     Bytecode = 0x21
	Cmpf     Bytecode = 0x22
	Writec   Bytecode = 0x23
	Readc    Bytecode = 0x24
	Exit     Bytecode = 0x25
)

// Each register is just a bit pattern with no concept of
// type (signed, unsigned int or float)
type Register uint32

// Laid out this way so that sizeof(Instruction) == 8
type Instruction struct {
	code Bytecode

	// additional data we can use for state
	// 		const: extra[0] tells const how many bytes to use to represent the constant
	extra [3]byte

	// argument to the bytecode itself
	arg uint32
}

type GVM struct {
	registers [NumRegisters]Register
	stack     [StackSize]byte
	program   []Instruction
	stdin     *bufio.Reader
}

// Constrains to types we can freely interpret their 32 bit pattern
type numeric32 interface {
	int32 | uint32 | float32
}

// Any numeric value including float, capped at 32 bits
type numeric interface {
	int8 | uint8 | int16 | uint16 | numeric32
}

// Only unsigned integers
type uinteger interface {
	uint8 | uint16 | uint32
}

const (
	NumRegisters int = 32
	StackSize    int = 65536
	// 4 bytes since our virtual architecture is 32-bit
	VArchBytes = 4
)

var (
	errProgramFinished    = errors.New("ran out of instructions")
	errStackUnderflow     = errors.New("stack underflow error")
	errStackOverflow      = errors.New("stack overflow error")
	errSegmentationFault  = errors.New("segmentation fault")
	errIllegalOperation   = errors.New("illegal operation at instruction")
	errUnknownInstruction = errors.New("instruction not recognized")
	errIO                 = errors.New("input-output error")

	// Maps from string -> instruction
	strToInstrMap = map[string]Bytecode{
		"nop":      Nop,
		"byte":     Byte,
		"const":    Const,
		"load":     Load,
		"store":    Store,
		"loadp8":   Loadp8,
		"loadp16":  Loadp16,
		"loadp32":  Loadp32,
		"storep8":  Storep8,
		"storep16": Storep16,
		"storep32": Storep32,
		"push":     Push,
		"pop":      Pop,
		"addi":     Addi,
		"addf":     Addf,
		"subi":     Subi,
		"subf":     Subf,
		"muli":     Muli,
		"mulf":     Mulf,
		"divi":     Divi,
		"divf":     Divf,
		"not":      Not,
		"and":      And,
		"or":       Or,
		"Xor":      Xor,
		"jmp":      Jmp,
		"jz":       Jz,
		"jnz":      Jnz,
		"jle":      Jle,
		"jl":       Jl,
		"jge":      Jge,
		"jg":       Jg,
		"cmpu":     Cmpu,
		"cmps":     Cmps,
		"cmpf":     Cmpf,
		"writec":   Writec,
		"readc":    Readc,
		"exit":     Exit,
	}

	// Maps from instruction -> string (built from strToInstrMap)
	instrToStrMap map[Bytecode]string

	// Allows us to replace \\* escape sequence with \*, such as \\n -> \n
	// (happens when reading from console or file)
	escapeSeqReplacements = map[string]string{
		"\\a":  "\a",
		"\\b":  "\b",
		"\\t":  "\t",
		"\\n":  "\n",
		"\\r":  "\r",
		"\\f":  "\f",
		"\\v":  "\v",
		"\\\"": "\"",
	}
)

// init is called when the package is first loaded (before main)
func init() {
	instrToStrMap = make(map[Bytecode]string, len(strToInstrMap))
	for s, b := range strToInstrMap {
		instrToStrMap[b] = s
	}
}

// Convert bytecode to string
func (b Bytecode) String() string {
	str, ok := instrToStrMap[b]
	if !ok {
		str = "?unknown?"
	}
	return str
}

// True if the bytecode requires an argument to be paired
// with it, such as const X
func (b Bytecode) RequiresOpArg() bool {
	return b == Const || b == Byte ||
		b == Push || b == Pop ||
		b == Jmp || b == Jz || b == Jnz || b == Jle || b == Jl || b == Jge || b == Jg
}

func NewInstruction(code Bytecode, arg uint32, extra ...byte) Instruction {
	instr := Instruction{code: code, arg: arg}
	size := min(len(extra), len(instr.extra))
	for i := 0; i < size; i++ {
		instr.extra[i] = extra[i]
	}
	return instr
}

func (i Instruction) String() string {
	if !i.code.RequiresOpArg() {
		return i.code.String()
	} else {
		intArg := int32(i.arg)
		if intArg < 0 {
			return fmt.Sprintf("%s %d (%d)", i.code.String(), intArg, i.arg)
		}
		return fmt.Sprintf("%s %d", i.code.String(), i.arg)
	}
}

func uint32FromBytes(bytes []byte) uint32 {
	return binary.LittleEndian.Uint32(bytes[:4])
}

func int32FromBytes(bytes []byte) int32 {
	return int32(uint32FromBytes(bytes))
}

func float32FromBytes(bytes []byte) float32 {
	return math.Float32frombits(uint32FromBytes(bytes[:4]))
}

func uint32ToBytes(u uint32, bytes []byte) {
	binary.LittleEndian.PutUint32(bytes[:4], u)
}

func float32ToBytes(f float32, bytes []byte) {
	uint32ToBytes(math.Float32bits(f), bytes)
}

func (vm *GVM) ProgramCounter() *Register {
	return &vm.registers[0]
}

func (vm *GVM) StackPointer() *Register {
	return &vm.registers[1]
}

func (vm *GVM) PrintCurrentState() {
	fmt.Println("->\t\tregisters:", vm.registers)
	// Prints the stack in reverse order, meaning the first element is actually the last
	// that will be removed
	fmt.Println("->\t\treverse stack:", vm.stack[0:*vm.StackPointer()])
}

func (vm *GVM) peekStack(offset uint32) []byte {
	sp := vm.StackPointer()
	return vm.stack[*sp-Register(offset) : *sp]
}

func (vm *GVM) popStack() []byte {
	sp := vm.StackPointer()
	start, end := *sp-Register(VArchBytes), *sp
	*sp = start
	return vm.stack[start:end]
}

func (vm *GVM) pushStackByte(value uint32) {
	sp := vm.StackPointer()
	start, end := *sp, *sp+Register(1)
	*sp = end
	vm.stack[start] = byte(value)
}

func (vm *GVM) pushStack(value any) {
	sp := vm.StackPointer()
	start, end := *sp, *sp+Register(VArchBytes)
	*sp = end
	switch v := value.(type) {
	case uint32:
		uint32ToBytes(v, vm.stack[start:end])
	case float32:
		float32ToBytes(v, vm.stack[start:end])
	default:
		panic("Unknown type: should be unsigned 32 bit or float 32 bit")
	}
}

func (vm *GVM) PrintProgram() {
	fmt.Println("-> program")
	for i, instr := range vm.program {
		fmt.Printf("\t%d: %s\n", i, instr)
	}
}

func compare[T numeric32](vm *GVM, convertFunc func([]byte) T) {
	arg0Bytes := vm.popStack()
	arg1Bytes := vm.peekStack(VArchBytes)

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

func arithmeticLogical(vm *GVM, op func([]byte, []byte)) {
	arg0Bytes := vm.popStack()
	arg1Bytes := vm.peekStack(VArchBytes)

	// Overwrites arg1Bytes with result of op
	op(arg0Bytes, arg1Bytes)
}

func loadpX[T numeric](vm *GVM) {
	addrBytes := vm.peekStack(VArchBytes)
	addr := uint32FromBytes(addrBytes)

	var v T
	sizeof := unsafe.Sizeof(v)
	result := uint32(0)

	switch sizeof {
	case 1:
		result = uint32(vm.stack[addr])
	case 2:
		result = uint32(binary.LittleEndian.Uint16(vm.stack[addr : addr+2]))
	case 4:
		result = uint32(binary.LittleEndian.Uint32(vm.stack[addr : addr+4]))
	}

	// overwrite addrBytes with memory value
	uint32ToBytes(result, addrBytes)
}

func storepX[T uinteger](vm *GVM) {
	addr := uint32FromBytes(vm.popStack())
	valueBytes := vm.popStack()

	var v T
	sizeof := uint32(unsafe.Sizeof(v))
	for i := uint32(0); i < sizeof; i++ {
		vm.stack[addr+i] = valueBytes[i]
	}
}

func (vm *GVM) execNextInstruction() error {
	pc := vm.ProgramCounter()
	if *pc >= Register(len(vm.program)) {
		return errProgramFinished
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
		// read register index from stack
		stackTop := vm.peekStack(VArchBytes)
		regIdx := uint32FromBytes(stackTop)
		// overwrite register index on stack with register value
		uint32ToBytes(uint32(vm.registers[regIdx]), stackTop)
	case Store:
		regIdx := uint32FromBytes(vm.popStack())
		if regIdx < 2 {
			// not allowed to write to program counter or stack pointer
			return errIllegalOperation
		}

		regValue := uint32FromBytes(vm.popStack())
		vm.registers[regIdx] = Register(regValue)
	case Loadp8:
		loadpX[uint8](vm)
	case Loadp16:
		loadpX[uint16](vm)
	case Loadp32:
		loadpX[uint32](vm)
	case Storep8:
		storepX[uint8](vm)
	case Storep16:
		storepX[uint16](vm)
	case Storep32:
		storepX[uint32](vm)
	case Push:
		sp := vm.StackPointer()
		*sp = *sp + Register(instr.arg)
	case Pop:
		sp := vm.StackPointer()
		*sp = *sp - Register(instr.arg)
	case Addi:
		arithmeticLogical(vm, arithAddi)
	case Addf:
		arithmeticLogical(vm, arithAddf)
	case Subi:
		arithmeticLogical(vm, arithSubi)
	case Subf:
		arithmeticLogical(vm, arithSubf)
	case Muli:
		arithmeticLogical(vm, arithMuli)
	case Mulf:
		arithmeticLogical(vm, arithMulf)
	case Divi:
		arithmeticLogical(vm, arithDivi)
	case Divf:
		arithmeticLogical(vm, arithDivf)
	case Not:
		arg := vm.peekStack(VArchBytes)
		// Invert all bits, store result in arg
		uint32ToBytes(^uint32FromBytes(arg), arg)
	case And:
		arithmeticLogical(vm, logicalAnd)
	case Or:
		arithmeticLogical(vm, logicalOr)
	case Xor:
		arithmeticLogical(vm, logicalXor)
	case Jmp:
		*pc = Register(instr.arg)
	case Jz:
		value := uint32FromBytes(vm.peekStack(VArchBytes))
		if value == 0 {
			*pc = Register(instr.arg)
		}
	case Jnz:
		value := uint32FromBytes(vm.peekStack(VArchBytes))
		if value != 0 {
			*pc = Register(instr.arg)
		}
	case Jle:
		value := uint32FromBytes(vm.peekStack(VArchBytes))
		if int32(value) <= 0 {
			*pc = Register(instr.arg)
		}
	case Jl:
		value := uint32FromBytes(vm.peekStack(VArchBytes))
		if int32(value) < 0 {
			*pc = Register(instr.arg)
		}
	case Jge:
		value := uint32FromBytes(vm.peekStack(VArchBytes))
		if int32(value) >= 0 {
			*pc = Register(instr.arg)
		}
	case Jg:
		value := uint32FromBytes(vm.peekStack(VArchBytes))
		if int32(value) > 0 {
			*pc = Register(instr.arg)
		}
	case Cmpu:
		compare(vm, uint32FromBytes)
	case Cmps:
		compare(vm, int32FromBytes)
	case Cmpf:
		compare(vm, float32FromBytes)
	case Writec:
		character := rune(uint32FromBytes(vm.popStack()))
		fmt.Print(string(character))
	case Readc:
		character, _, err := vm.stdin.ReadRune()
		if err != nil {
			return errIO
		}
		vm.pushStack(uint32(character))
	case Exit:
		// Sets the pc to be one after the last instruction
		*pc = Register(len(vm.program))
	default:
		return errUnknownInstruction
	}

	return nil
}

func NewVirtualMachine(files ...string) (*GVM, error) {
	vm := &GVM{stdin: bufio.NewReader(os.Stdin)}
	file, err := os.Open(files[0])
	if err != nil {
		fmt.Println("Could not read", files[0])
		return nil, err
	}

	labels := make(map[string]string)
	lines := make([]string, 0)
	reader := bufio.NewReader(file)

	// Allows us to easily find and replace commands from start to end of line
	comments := regexp.MustCompile("//.*")

	// First preprocess line to remove whitespace lines and convert labels
	// into line numbers
	for {
		line, _, err := reader.ReadLine()
		if err != nil {
			break
		}

		lines = preprocessLine(string(line), comments, labels, lines)
	}

	vm.program = make([]Instruction, 0, len(lines))

	for _, line := range lines {
		// Replace all labels with their instruction address
		for label, lineNum := range labels {
			line = strings.ReplaceAll(line, label, lineNum)
		}

		instrs, err := parseInputLine(line)
		if err != nil {
			return nil, err
		}

		vm.program = append(vm.program, instrs...)
	}

	return vm, nil
}

func preprocessLine(line string, comments *regexp.Regexp, labels map[string]string, lines []string) []string {
	line = comments.ReplaceAllString(line, "")
	line = strings.TrimSpace(line)

	// Check if the line was pure whitespace
	if line == "" {
		return lines
		// Check if the line is a label
	} else if strings.HasSuffix(line, ":") {
		// Get rid of the : in the label
		line = strings.ReplaceAll(line, ":", "")
		labels[line] = fmt.Sprintf("%d", len(lines))
		return append(lines, "nop")
	} else {
		return append(lines, line)
	}
}

func insertEscapeSeqReplacements(line string) string {
	for orig, replace := range escapeSeqReplacements {
		line = strings.ReplaceAll(line, orig, replace)
	}
	return line
}

func parseInputLine(line string) ([]Instruction, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return []Instruction{{code: Nop}}, nil
	}

	parsed := strings.Split(line, " ")
	code, ok := strToInstrMap[parsed[0]]
	if !ok {
		return nil, fmt.Errorf("unknown bytecode: %s", parsed[0])
	}

	if len(parsed) > 1 {
		// const is a character
		if strings.HasPrefix(parsed[1], "'") {
			if !strings.HasSuffix(parsed[1], "'") {
				return nil, errors.New("invalid syntax: unterminated character")
			}

			parsed[1] = insertEscapeSeqReplacements(parsed[1])
			runes := []rune(parsed[1])
			if len(runes) != 3 {
				return nil, errors.New("character value to large to fit into 32 bits")
			}

			return []Instruction{NewInstruction(code, uint32(runes[1]))}, nil
		} else if strings.HasPrefix(parsed[1], "\"") {
			if code != Const {
				return nil, errors.New("string constant used outside of const bytecode")
			}

			if !strings.HasSuffix(parsed[1], "\"") {
				return nil, errors.New("invalid syntax: unterminated string")
			}

			parsed[1] = insertEscapeSeqReplacements(parsed[1])
			bytes := []byte(parsed[1])
			// Slice of bytes to get rid of start and end quotes
			bytes = bytes[1 : len(bytes)-1]

			instrs := make([]Instruction, len(bytes))
			// Expand const "string" instruction into a series of 1-byte consts in reverse order
			for i := 0; i < len(instrs); i++ {
				value := uint32(bytes[len(bytes)-1-i])
				// Use 1 byte to represent each const by using Byte
				instrs[i] = NewInstruction(Byte, value)
			}

			return instrs, nil
		} else {
			// Likely a regular number or float
			if strings.Contains(parsed[1], ".") {
				arg, err := strconv.ParseFloat(parsed[1], 32)
				if err != nil {
					return nil, err
				}

				return []Instruction{NewInstruction(code, math.Float32bits(float32(arg)))}, nil
			} else {
				arg, err := strconv.ParseInt(parsed[1], 10, 32)
				if err != nil {
					return nil, err
				}

				return []Instruction{NewInstruction(code, uint32(arg))}, nil
			}
		}
	} else {
		return []Instruction{NewInstruction(code, 0)}, nil
	}
}

func main() {
	var is Instruction
	if unsafe.Sizeof(is) != 8 {
		panic("Critical error: instruction struct is not 8 bytes")
	}

	vm, err := NewVirtualMachine("examples/test.b")
	if err != nil {
		fmt.Println(err)
		return
	}

	for {
		err = vm.execNextInstruction()
		if err != nil {
			break
		}
	}

	// Insert final newline before returning
	fmt.Println()
}