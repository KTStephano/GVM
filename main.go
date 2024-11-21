package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"flag"
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
			- supports single stepping through instructions
			- supports setting program breakpoints

	The stack is 64kb in size minimum

	Possible bytecodes
			nop   (no operation)
			sp 	  (pushes value of stack pointer onto the stack)
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

type Bytecode byte

const (
	Nop      Bytecode = 0x00
	Sp       Bytecode = 0x01
	Byte     Bytecode = 0x02
	Const    Bytecode = 0x03
	Load     Bytecode = 0x04
	Store    Bytecode = 0x05
	Loadp8   Bytecode = 0x06
	Loadp16  Bytecode = 0x07
	Loadp32  Bytecode = 0x08
	Storep8  Bytecode = 0x09
	Storep16 Bytecode = 0x0A
	Storep32 Bytecode = 0x0B
	Push     Bytecode = 0x0C
	Pop      Bytecode = 0x0D
	Addi     Bytecode = 0x0E
	Addf     Bytecode = 0x0F
	Subi     Bytecode = 0x10
	Subf     Bytecode = 0x11
	Muli     Bytecode = 0x12
	Mulf     Bytecode = 0x13
	Divi     Bytecode = 0x14
	Divf     Bytecode = 0x15
	Not      Bytecode = 0x16
	And      Bytecode = 0x17
	Or       Bytecode = 0x18
	Xor      Bytecode = 0x19
	Jmp      Bytecode = 0x1A
	Jz       Bytecode = 0x1B
	Jnz      Bytecode = 0x1C
	Jle      Bytecode = 0x1D
	Jl       Bytecode = 0x1E
	Jge      Bytecode = 0x1F
	Jg       Bytecode = 0x20
	Cmpu     Bytecode = 0x21
	Cmps     Bytecode = 0x22
	Cmpf     Bytecode = 0x23
	Writec   Bytecode = 0x24
	Readc    Bytecode = 0x25
	Exit     Bytecode = 0x26
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

// We store these if we want extra debug information
type DebugSymbols struct {
	// maps from line num -> source
	source map[int]string
}

type GVM struct {
	registers [NumRegisters]Register
	stack     [StackSize]byte
	program   []Instruction

	// Allows vm to read/write to some type of output
	stdout *bufio.Writer
	stdin  *bufio.Reader

	// This gets written to whenever program encounters a normal or critical error
	errcode error

	// Debug data
	debugOut *strings.Builder
	debugSym *DebugSymbols
}

// Constrains to types we can freely interpret their 32 bit pattern
type numeric32 interface {
	int32 | uint32 | float32
}

const (
	NumRegisters int = 32
	StackSize    int = 65536
	// 4 bytes since our virtual architecture is 32-bit
	VArchBytes = 4
)

var (
	errProgramFinished    = errors.New("ran out of instructions")
	errSegmentationFault  = errors.New("segmentation fault")
	errIllegalOperation   = errors.New("illegal operation at instruction")
	errUnknownInstruction = errors.New("instruction not recognized")
	errIO                 = errors.New("input-output error")

	// Maps from string -> instruction
	strToInstrMap = map[string]Bytecode{
		"nop":      Nop,
		"sp":       Sp,
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

	// Allows us to go into debug mode when needed
	debugVM = flag.Bool("debug", false, "Enter into single-step debug mode")
)

// init is called when the package is first loaded (before main)
func init() {
	// Parse command line
	flag.Parse()

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
	return b == Const || b == Byte
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

func (vm *GVM) ProgramCounter() *Register {
	return &vm.registers[0]
}

func (vm *GVM) StackPointer() *Register {
	return &vm.registers[1]
}

func (vm *GVM) PrintCurrentState() {
	pc := vm.ProgramCounter()
	if *pc < Register(len(vm.program)) {
		fmtStr := "->\t\tnext instruction> %d: %s\n"
		if vm.debugSym != nil {
			fmt.Printf(fmtStr, *pc, vm.debugSym.source[int(*pc)])
		} else {
			fmt.Printf(fmtStr, *pc, vm.program[*pc])
		}
	}

	fmt.Println("->\t\tregisters>", vm.registers)
	// Prints the stack in reverse order, meaning the first element is actually the last
	// that will be removed
	fmt.Println("->\t\treverse stack>", vm.stack[0:*vm.StackPointer()])

	if vm.debugOut != nil {
		fmt.Println("->\t\toutput>", revertEscapeSeqReplacements(vm.debugOut.String()))
	}
}

func (vm *GVM) PrintProgram() {
	for i, instr := range vm.program {
		if vm.debugSym != nil {
			fmt.Printf("%d: %s\n", i, vm.debugSym.source[i])
		} else {
			fmt.Printf("%d: %s\n", i, instr)
		}
	}
}

func NewVirtualMachine(debug bool, files ...string) (GVM, error) {
	vm := GVM{stdin: bufio.NewReader(os.Stdin)}

	// If requested, set up the VM in debug mode
	var debugSymMap map[int]string
	if debug {
		debugSymMap = make(map[int]string)
		vm.debugOut = &strings.Builder{}
		vm.debugSym = &DebugSymbols{source: debugSymMap}
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
			return vm, err
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
			return vm, err
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
			return vm, err
		}

		vm.program = append(vm.program, instrs...)
	}

	return vm, nil
}

func (vm *GVM) ExecNextInstruction() {
	pc := vm.ProgramCounter()
	if *pc >= Register(len(vm.program)) {
		vm.errcode = errProgramFinished
		return
	}

	instr := vm.program[*pc]
	*pc += 1

	switch instr.code {
	case Nop:
	case Sp:
		vm.pushStack(uint32(*vm.StackPointer()))
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
			vm.errcode = errIllegalOperation
			return
		}

		regValue := uint32FromBytes(vm.popStack())
		vm.registers[regIdx] = Register(regValue)
	case Loadp8:
		loadpX(vm, 1)
	case Loadp16:
		loadpX(vm, 2)
	case Loadp32:
		loadpX(vm, 4)
	case Storep8:
		storepX(vm, 1)
	case Storep16:
		storepX(vm, 2)
	case Storep32:
		storepX(vm, 4)
	case Push:
		bytes := uint32FromBytes(vm.popStack())
		sp := vm.StackPointer()
		*sp = *sp + Register(bytes)
	case Pop:
		bytes := uint32FromBytes(vm.popStack())
		sp := vm.StackPointer()
		*sp = *sp - Register(bytes)
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
		addr := uint32FromBytes(vm.popStack())
		*pc = Register(addr)
	case Jz:
		addr := uint32FromBytes(vm.popStack())
		value := uint32FromBytes(vm.peekStack(VArchBytes))
		if value == 0 {
			*pc = Register(addr)
		}
	case Jnz:
		addr := uint32FromBytes(vm.popStack())
		value := uint32FromBytes(vm.peekStack(VArchBytes))
		if value != 0 {
			*pc = Register(addr)
		}
	case Jle:
		addr := uint32FromBytes(vm.popStack())
		value := uint32FromBytes(vm.peekStack(VArchBytes))
		if int32(value) <= 0 {
			*pc = Register(addr)
		}
	case Jl:
		addr := uint32FromBytes(vm.popStack())
		value := uint32FromBytes(vm.peekStack(VArchBytes))
		if int32(value) < 0 {
			*pc = Register(addr)
		}
	case Jge:
		addr := uint32FromBytes(vm.popStack())
		value := uint32FromBytes(vm.peekStack(VArchBytes))
		if int32(value) >= 0 {
			*pc = Register(addr)
		}
	case Jg:
		addr := uint32FromBytes(vm.popStack())
		value := uint32FromBytes(vm.peekStack(VArchBytes))
		if int32(value) > 0 {
			*pc = Register(addr)
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

func (vm *GVM) peekStack(offset uint32) []byte {
	sp := vm.StackPointer()
	return vm.stack[*sp-Register(offset):]
}

func (vm *GVM) popStack() []byte {
	sp := vm.StackPointer()
	start := *sp - Register(VArchBytes)
	*sp = start
	return vm.stack[start:]
}

func (vm *GVM) pushStackByte(value uint32) {
	sp := vm.StackPointer()
	start := *sp
	*sp++
	vm.stack[start] = byte(value)
}

func (vm *GVM) pushStack(value uint32) {
	sp := vm.StackPointer()
	start := *sp
	*sp += Register(VArchBytes)
	uint32ToBytes(value, vm.stack[start:])
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

func loadpX(vm *GVM, sizeof uint32) {
	addrBytes := vm.peekStack(VArchBytes)
	addr := uint32FromBytes(addrBytes)

	result := uint32(0)
	switch sizeof {
	case 1:
		result = uint32(vm.stack[addr])
	case 2:
		result = uint32(binary.LittleEndian.Uint16(vm.stack[addr:]))
	case 4:
		result = uint32(binary.LittleEndian.Uint32(vm.stack[addr:]))
	}

	// overwrite addrBytes with memory value
	uint32ToBytes(result, addrBytes)
}

func storepX(vm *GVM, sizeof uint32) {
	addr := uint32FromBytes(vm.popStack())
	valueBytes := vm.popStack()

	for i := uint32(0); i < sizeof; i++ {
		vm.stack[addr+i] = valueBytes[i]
	}
}

// Checks for things like \\n and replaces it with \n
func insertEscapeSeqReplacements(line string) string {
	for orig, replace := range escapeSeqReplacements {
		line = strings.ReplaceAll(line, orig, replace)
	}
	return line
}

// Checks for things like \n and replaces it with \\n
func revertEscapeSeqReplacements(line string) string {
	for orig, replace := range escapeSeqReplacements {
		line = strings.ReplaceAll(line, replace, orig)
	}
	return line
}

func preprocessLine(line string, comments *regexp.Regexp, labels map[string]string, lines [][2]string, debugSym map[int]string) ([][2]string, error) {
	line = comments.ReplaceAllString(line, "")
	line = strings.TrimSpace(line)

	// Check if the line was pure whitespace
	if line == "" {
		return lines, nil
		// Check if the line is a label
	} else if strings.HasSuffix(line, ":") {
		// Get rid of the : in the label
		label := strings.ReplaceAll(line, ":", "")
		labels[label] = fmt.Sprintf("%d", len(lines))
		if debugSym != nil {
			debugSym[len(lines)] = label
		}
		return append(lines, [2]string{"nop", ""}), nil
	} else {
		split := strings.Split(line, " ")
		code := split[0]
		args := ""
		if len(split) > 1 {
			// Rejoin rest of split array into 1 string
			args = strings.Join(split[1:], " ")

			// If it starts with a double or single quote, insert escape sequence replacements
			if strings.HasPrefix(args, "'") || strings.HasPrefix(args, "\"") {
				// Make sure the double or single quote also includes a terminating quote
				if !strings.HasSuffix(args, "'") && !strings.HasSuffix(args, "\"") {
					return nil, errors.New("unterminated character or string")
				}

				args = insertEscapeSeqReplacements(args)
			}
		}

		// If the instruction is `const arg` and the argument is a string,
		// expand the instruction to be a series of `byte arg` instructions
		//
		// We need to do the expansion in the preprocess stage or the labels
		// will end up pointing to the wrong instructions
		if code == Const.String() && strings.HasPrefix(args, "\"") && strings.HasSuffix(args, "\"") {
			bytes := []byte(args)
			// Slice bytes to get rid of start and end quotes
			bytes = bytes[1 : len(bytes)-1]

			// Append instructions in reverse order so that the top value on the
			// stack corresponds to the start of the string
			for i := len(bytes) - 1; i >= 0; i-- {
				if debugSym != nil {
					// Since it's a debug symbol, add back the escaped characters
					debugSym[len(lines)] = revertEscapeSeqReplacements(fmt.Sprintf("%s '%c'", Byte.String(), bytes[i]))
				}
				lines = append(lines, [2]string{Byte.String(), fmt.Sprintf("%d", bytes[i])})
			}
		} else {
			if debugSym != nil {
				debugSym[len(lines)] = line
			}
			lines = append(lines, [2]string{code, args})
		}

		return lines, nil
	}
}

func parseInputLine(line [2]string) ([]Instruction, error) {
	code, ok := strToInstrMap[line[0]]
	if !ok {
		return nil, fmt.Errorf("unknown bytecode: %s", line[0])
	}

	strArg := line[1]
	if strArg != "" {
		// Character - replace with number
		if strings.HasPrefix(strArg, "'") {
			runes := []rune(strArg)
			// first rune should be quote, then number, then end quote (len == 3)
			if len(runes) != 3 {
				return nil, errors.New("character is too large to fit into 32 bits")
			}

			return []Instruction{NewInstruction(code, uint32(runes[1]))}, nil
		} else {
			// Likely a regular number or float
			if strings.Contains(strArg, ".") {
				arg, err := strconv.ParseFloat(strArg, 32)
				if err != nil {
					return nil, err
				}

				return []Instruction{NewInstruction(code, math.Float32bits(float32(arg)))}, nil
			} else {
				var arg int64
				var err error
				base := 10
				// Check for hex values
				if strings.HasPrefix(strArg, "0x") {
					base = 16
					// Remove 0x from input
					strArg = strings.Replace(strArg, "0x", "", 1)
				}

				arg, err = strconv.ParseInt(strArg, base, 32)
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

func execProgramDebugMode(vm *GVM) {
	fmt.Printf("Commands:\n\tn or next: execute next instruction\n\tr or run: run program\n\tb or break <line>: break on line (or remove break on line)\n\n")

	vm.PrintCurrentState()

	reader := bufio.NewReader(os.Stdin)
	waitForInput := true
	breakAtLines := make(map[int]struct{})
	lastBreakLine := -1
	for {
		line := ""
		if waitForInput {
			fmt.Print("->")
			line, _ = reader.ReadString('\n')
			line = strings.ToLower(strings.TrimSpace(line))
		} else {
			// Check if we've reached a breakpoint
			currInstruction := int(*vm.ProgramCounter())
			if _, ok := breakAtLines[currInstruction]; lastBreakLine != currInstruction && ok {
				fmt.Println("breakpoint")
				vm.PrintCurrentState()

				waitForInput = true
				lastBreakLine = currInstruction
				continue
			}
		}

		if !waitForInput || line == "n" || line == "next" {
			// Reset break flag
			lastBreakLine = -1

			vm.ExecNextInstruction()
			if waitForInput {
				// Only print state after each instruction if we're also waiting for input
				// after each instruction
				vm.PrintCurrentState()
			}

			if vm.errcode != nil {
				fmt.Println("output>", revertEscapeSeqReplacements(vm.debugOut.String()))
				fmt.Println(vm.errcode)
				return
			}
		} else if line == "program" {
			vm.PrintProgram()
		} else if line == "r" || line == "run" {
			waitForInput = false
		} else if strings.HasPrefix(line, "b") {
			arg := strings.Join(strings.Split(line, " ")[1:], " ")
			line, err := strconv.ParseInt(arg, 10, 32)
			if err != nil {
				fmt.Println("Unknown line number:", err)
			} else {
				_, ok := breakAtLines[int(line)]
				// If the line number has already been added, remove it
				if ok {
					delete(breakAtLines, int(line))
				} else {
					// Otherwise add it now
					breakAtLines[int(line)] = struct{}{}
				}
			}
		}
	}
}

func main() {
	var is Instruction
	if unsafe.Sizeof(is) != 8 {
		panic("Critical error: instruction struct is not 8 bytes")
	}

	args := os.Args[len(os.Args)-flag.NArg():]

	// Use os.Args to accept list of files. This should still work when/if
	// additional arguments are added (at that point using package flag) because
	// it will tell us how many arguments are remaining after it has finished parsing.
	//
	// First argument is the path to the program
	if len(args) == 0 {
		fmt.Println("Usage: <file 1> [file 2] [file 3] ... [file N]")
		return
	}

	vm, err := NewVirtualMachine(*debugVM, args...)
	if err != nil {
		fmt.Println(err)
		return
	}

	// Allows us to handle critical errors that came up during execuion
	defer func() {
		if r := recover(); r != nil {
			if vm.errcode != nil {
				fmt.Println(vm.errcode)
			} else {
				fmt.Println(errSegmentationFault)
			}
		}
	}()

	if *debugVM {
		execProgramDebugMode(&vm)
	} else {
		for {
			vm.ExecNextInstruction()
			if vm.errcode != nil {
				if vm.errcode != errProgramFinished {
					fmt.Print(vm.errcode)
				}
				break
			}
		}
	}
}
