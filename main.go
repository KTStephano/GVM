package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
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

	The stack is 64kb in size

	Possible bytecodes
		nop (no operation)
		const (pushes const value onto stack (can be a label))
		load (loads value of register at index stack[0])
		store (stores value of stack[1] to register at index stack[0])
		loadp8, loadp16, loadp32 (loads 8, 16 or 32 bit value from address at stack[0], widens to 32 bits)
		storep8, storep16, storep32 (narrows stack[1] to 8, 16 or 32 bits and writes it to address at stack[0])
		push (reserve bytes on the stack, advances stack pointer)
		pop (free bytes back to the stack, retracts stack pointer)
		addi, addf (int and float add)
		subi, subf (int and float sub)
		muli, mulf (int and float mul)
		divi, divf (int and float div)
		jmp (unconditional jump to program address at stack[0])
		jz  (jump if stack[0] is 0)
		jnz (jump if stack[0] is not 0)
		jle (jump if stack[0] less than or equal to 0)
		jl  (jump if stack[0] less than 0)
		jge (jump if stack[0] greater than or equal to 0)
		jg  (jump if stack[0] greater than 0)

		// The following all do: (compare stack[0] to stack[1]: negative if stack[0] < stack[1], 0 if stack[0] == stack[1], positive if stack[0] > stack[1])
		// However, the naming scheme is as follows:
		//		cmpu -> treats both inputs as unsigned 32-bit
		//		cmps -> treats both inputs as signed 32-bit
		//		cmpf -> treats both inputs as float 32-bit
		cmpu
		cmps
		cmpf

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
	Const    Bytecode = 0x01
	Load     Bytecode = 0x02
	Store    Bytecode = 0x03
	Loadp8   Bytecode = 0x04
	Loadp16  Bytecode = 0x05
	Loadp32  Bytecode = 0x06
	Storep8  Bytecode = 0x07
	Storep16 Bytecode = 0x08
	Storep32 Bytecode = 0x09
	Push     Bytecode = 0x0A
	Pop      Bytecode = 0x0B
	Addi     Bytecode = 0x0C
	Addf     Bytecode = 0x0D
	Subi     Bytecode = 0x0E
	Subf     Bytecode = 0x0F
	Muli     Bytecode = 0x10
	Mulf     Bytecode = 0x11
	Divi     Bytecode = 0x12
	Divf     Bytecode = 0x13
	Jmp      Bytecode = 0x14
	Jz       Bytecode = 0x15
	Jnz      Bytecode = 0x16
	Jle      Bytecode = 0x17
	Jl       Bytecode = 0x18
	Jge      Bytecode = 0x19
	Jg       Bytecode = 0x1A
	Cmpu     Bytecode = 0x1B
	Cmps     Bytecode = 0x1C
	Cmpf     Bytecode = 0x1D
)

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

	instrMap = map[string]Bytecode{
		"nop":      Nop,
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
	}
)

// Convert bytecode to string
func (b Bytecode) String() string {
	switch b {
	case Nop:
		return "nop"
	case Const:
		return "const"
	case Load:
		return "load"
	case Store:
		return "store"
	case Loadp8:
		return "loadp8"
	case Loadp16:
		return "loadp16"
	case Loadp32:
		return "loadp32"
	case Storep8:
		return "storep8"
	case Storep16:
		return "storep16"
	case Storep32:
		return "storep32"
	case Push:
		return "push"
	case Pop:
		return "pop"
	case Addi:
		return "addi"
	case Addf:
		return "addf"
	case Subi:
		return "subi"
	case Subf:
		return "subf"
	case Muli:
		return "muli"
	case Mulf:
		return "mulf"
	case Divi:
		return "divi"
	case Divf:
		return "divf"
	case Jmp:
		return "jmp"
	case Jz:
		return "jz"
	case Jnz:
		return "jnz"
	case Jle:
		return "jle"
	case Jl:
		return "jl"
	case Jge:
		return "jge"
	case Jg:
		return "jg"
	case Cmpu:
		return "cmpu"
	case Cmps:
		return "cmps"
	case Cmpf:
		return "cmpf"
	default:
		return "?unknown?"
	}
}

// True if the bytecode requires an argument to be paired
// with it, such as const X
func (b Bytecode) RequiresOpArg() bool {
	return b == Const ||
		b == Push || b == Pop ||
		b == Jmp || b == Jz || b == Jnz || b == Jle || b == Jl || b == Jge || b == Jg
}

// Each register is just a bit pattern with no concept of
// type (signed, unsigned int or float)
type Register uint32

type Instruction struct {
	code Bytecode
	arg  uint32
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

type GVM struct {
	registers [NumRegisters]Register
	stack     [StackSize]byte
	program   []Instruction
}

func uint32FromBytes(bytes []byte) uint32 {
	return binary.LittleEndian.Uint32(bytes[:4])
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
	fmt.Print("->\t\tstack: [")
	for i := int32(*vm.StackPointer()) - 1; i >= 0; i-- {
		if i == 0 {
			fmt.Printf("%d", vm.stack[i])
		} else {
			fmt.Printf("%d ", vm.stack[i])
		}
	}
	fmt.Println("]")
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

// Constrains to types we can freely interpret their 32 bit pattern
type numeric32 interface {
	int32 | uint32 | float32
}

type numeric interface {
	int8 | uint8 | int16 | uint16 | numeric32
}

type uinteger interface {
	uint8 | uint16 | uint32
}

func compare[T numeric32](vm *GVM) {
	arg0 := uint32FromBytes(vm.popStack())
	arg1Bytes := vm.peekStack(VArchBytes)
	arg1 := uint32FromBytes(arg1Bytes)

	a0T := T(arg0)
	a1T := T(arg1)
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

func arithAddi(x, y uint32, bytes []byte) {
	// Overwrite bytes with result
	uint32ToBytes(x+y, bytes)
}

func arithAddf(x, y float32, bytes []byte) {
	// Overwrite bytes with result
	float32ToBytes(x+y, bytes)
}

func arithSubi(x, y uint32, bytes []byte) {
	// Overwrite bytes with result
	uint32ToBytes(x-y, bytes)
}

func arithSubf(x, y float32, bytes []byte) {
	// Overwrite bytes with result
	float32ToBytes(x-y, bytes)
}

func arithMuli(x, y uint32, bytes []byte) {
	// Overwrite bytes with result
	uint32ToBytes(x*y, bytes)
}

func arithMulf(x, y float32, bytes []byte) {
	// Overwrite bytes with result
	float32ToBytes(x*y, bytes)
}

func arithDivi(x, y uint32, bytes []byte) {
	// Overwrite bytes with result
	uint32ToBytes(x/y, bytes)
}

func arithDivf(x, y float32, bytes []byte) {
	// Overwrite bytes with result
	float32ToBytes(x/y, bytes)
}

func arithmetic[T numeric32](vm *GVM, op func(T, T, []byte)) {
	arg0 := uint32FromBytes(vm.popStack())
	arg1Bytes := vm.peekStack(VArchBytes)
	arg1 := uint32FromBytes(arg1Bytes)

	// Overwrites arg1Bytes with result of op
	op(T(arg0), T(arg1), arg1Bytes)
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

func (vm *GVM) execNextInstruction(debug bool) error {
	pc := vm.ProgramCounter()
	if *pc >= Register(len(vm.program)) {
		return errProgramFinished
	}

	instr := vm.program[*pc]
	*pc += 1
	switch instr.code {
	case Nop:
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
	case Addi:
		arithmetic(vm, arithAddi)
	case Addf:
		arithmetic(vm, arithAddf)
	case Subi:
		arithmetic(vm, arithSubi)
	case Subf:
		arithmetic(vm, arithSubf)
	case Muli:
		arithmetic(vm, arithMuli)
	case Mulf:
		arithmetic(vm, arithMulf)
	case Divi:
		arithmetic(vm, arithDivi)
	case Divf:
		arithmetic(vm, arithDivf)
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
		compare[uint32](vm)
	case Cmps:
		compare[int32](vm)
	case Cmpf:
		compare[float32](vm)
	default:
		return errUnknownInstruction
	}

	if debug {
		vm.PrintCurrentState()
	}

	return nil
}

func NewVirtualMachine(file string) (*GVM, error) {
	return &GVM{program: make([]Instruction, 0)}, nil
}

func parseInputLine(line string) (Instruction, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return Instruction{code: Nop}, nil
	}

	parsed := strings.Split(line, " ")
	code, ok := instrMap[parsed[0]]
	if !ok {
		return Instruction{}, fmt.Errorf("unknown bytecode: %s", parsed[0])
	}

	if len(parsed) > 1 {
		// const is a character
		if strings.HasPrefix(parsed[1], "'") {
			runes := []rune(parsed[1])
			if len(runes) < 3 {
				return Instruction{}, errors.New("invalid syntax: unterminated character")
			} else if len(runes) != 3 {
				return Instruction{}, errors.New("character value to large to fit into 32 bits")
			}

			return Instruction{code: code, arg: uint32(runes[1])}, nil
		} else {
			arg, err := strconv.ParseInt(parsed[1], 10, 32)
			if err != nil {
				return Instruction{}, err
			}

			return Instruction{code: code, arg: uint32(arg)}, nil
		}
	} else {
		return Instruction{code: code}, nil
	}
}

func main() {
	reader := bufio.NewReader(os.Stdin)
	vm, err := NewVirtualMachine("")
	if err != nil {
		fmt.Println(err)
		return
	}

	for {
		fmt.Print("-> ")
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "program" {
			vm.PrintProgram()
		} else {
			instr, err := parseInputLine(line)
			if err != nil {
				fmt.Println(err)
			} else {
				vm.program = append(vm.program, instr)
				for {
					if e := vm.execNextInstruction(true); e != nil {
						break
					}
				}
			}
		}
	}
}
