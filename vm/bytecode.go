package gvm

import (
	"fmt"
	"unsafe"
)

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

var (
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
)

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

// This is called when package is first loaded (before main)
func init() {
	var instr Instruction
	if unsafe.Sizeof(instr) != 8 {
		panic("Instruction struct size not equal to 8")
	}

	// Build instruction -> string map using the existing string -> instruction map
	instrToStrMap = make(map[Bytecode]string, len(strToInstrMap))
	for s, b := range strToInstrMap {
		instrToStrMap[b] = s
	}
}
