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
)

/*
	For each CPU core:
		- 32-bit virtual architecture
		- 64 registers starting at index 0
		- register 0 is the program counter
		- register 1 is the stack pointer
		- registers indexed 2 through 63 are general purpose, 32-bit

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
		cmp (compare stack[0] to stack[1]: negative if stack[0] < stack[1], 0 if stack[0] == stack[1], positive if stack[0] > stack[1])

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
	Cmp      Bytecode = 0x1B
)

const (
	NumRegisters int = 64
	StackSize    int = 65536
	// 4 bytes since our virtual architecture is 32-bit
	VArchBytes = 4
)

var (
	errProgramFinished   = errors.New("ran out of instructions")
	errStackUnderflow    = errors.New("stack underflow error")
	errStackOverflow     = errors.New("stack overflow error")
	errSegmentationFault = errors.New("segmentation fault")
	errNotYetImplemented = errors.New("instruction not implemented")

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
		"cmp":      Cmp,
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
	case Cmp:
		return "cmp"
	default:
		return "?unknown?"
	}
}

// Each register is just a bit pattern with no concept of
// type (signed, unsigned int or float)
type Register uint32

const NoInstructionArg uint32 = math.MaxUint32

type Instruction struct {
	code Bytecode
	arg  uint32
}

func (i Instruction) String() string {
	if i.arg == NoInstructionArg {
		return i.code.String()
	} else {
		return fmt.Sprintf("%s %d", i.code.String(), i.arg)
	}
}

type GVM struct {
	registers [NumRegisters]Register
	stack     [StackSize]byte
	program   []Instruction
}

func uint32FromBytes(bytes []byte) uint32 {
	return binary.NativeEndian.Uint32(bytes[:4])
}

func float32FromBytes(bytes []byte) float32 {
	return math.Float32frombits(uint32FromBytes(bytes[:4]))
}

func uint32ToBytes(u uint32, bytes []byte) {
	binary.NativeEndian.PutUint32(bytes[:4], u)
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
	fmt.Println("->\t\tstack:", vm.stack[0:*vm.StackPointer()])
}

func (vm *GVM) PrintProgram() {
	fmt.Println("-> program")
	for i, instr := range vm.program {
		fmt.Printf("\t%d: %s\n", i, instr)
	}
}

func (vm *GVM) runNextInstruction(debug bool) error {
	pc := vm.ProgramCounter()
	sp := vm.StackPointer()
	if *pc >= Register(len(vm.program)) {
		return errProgramFinished
	}

	instr := vm.program[*pc]
	*pc += 1
	switch instr.code {
	case Nop:
	case Const:
		start, end := *sp, *sp+Register(VArchBytes)
		uint32ToBytes(instr.arg, vm.stack[start:end])
		*sp = end
	case Load:
		// determine start/end bytes
		start, end := *sp-Register(VArchBytes), *sp
		// read register index from stack
		regIdx := uint32FromBytes(vm.stack[start:end])
		// overwrite register index on stack with register value
		uint32ToBytes(uint32(vm.registers[regIdx]), vm.stack[start:end])
	case Store:
		storeIdx := *sp - 2*Register(VArchBytes)
		regIdx := *sp - Register(VArchBytes)

		storeVal := uint32FromBytes(vm.stack[storeIdx : storeIdx+VArchBytes])
		regIdx = Register(uint32FromBytes(vm.stack[regIdx : regIdx+VArchBytes]))

		vm.registers[regIdx] = Register(storeVal)
		*sp = storeIdx
	case Jmp:
		*pc = Register(instr.arg)
	case Jnz:
		start, end := *sp-Register(VArchBytes), *sp
		stackVal := uint32FromBytes(vm.stack[start:end])
		*sp = start
		if stackVal != 0 {
			*pc = Register(instr.arg)
		}
	case Addi:
		arg1Start := *sp - 2*Register(VArchBytes)
		arg0Start := *sp - Register(VArchBytes)

		arg1 := uint32FromBytes(vm.stack[arg1Start : arg1Start+VArchBytes])
		arg0 := uint32FromBytes(vm.stack[arg0Start : arg0Start+VArchBytes])

		uint32ToBytes(arg0+arg1, vm.stack[arg1Start:arg1Start+VArchBytes])
		*sp = arg1Start + VArchBytes
	default:
		return errNotYetImplemented
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
		return Instruction{code: Nop, arg: NoInstructionArg}, nil
	}

	parsed := strings.Split(line, " ")
	code, ok := instrMap[parsed[0]]
	if !ok {
		return Instruction{}, fmt.Errorf("unknown bytecode: %s", parsed[0])
	}

	if len(parsed) > 1 {
		arg, err := strconv.ParseInt(parsed[1], 10, 32)
		if err != nil {
			return Instruction{}, err
		}

		return Instruction{code: code, arg: uint32(arg)}, nil
	} else {
		return Instruction{code: code, arg: NoInstructionArg}, nil
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
					if e := vm.runNextInstruction(true); e != nil {
						break
					}
				}
			}
		}
	}
}
