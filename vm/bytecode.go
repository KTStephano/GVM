package gvm

import (
	"fmt"
)

type Bytecode byte

const (
	Nop      Bytecode = 0x00
	Byte     Bytecode = 0x01
	Const    Bytecode = 0x02
	Load     Bytecode = 0x03
	Store    Bytecode = 0x04
	Kstore   Bytecode = 0x05
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
	Writeb   Bytecode = 0x24
	Writec   Bytecode = 0x25
	Flush    Bytecode = 0x26
	Readc    Bytecode = 0x27
	Exit     Bytecode = 0x28
)

type Instruction struct {
	code uint32
	arg  uint32
}

var (
	// Maps from string -> instruction
	strToInstrMap = map[string]Bytecode{
		"nop":      Nop,
		"byte":     Byte,
		"const":    Const,
		"load":     Load,
		"store":    Store,
		"kstore":   Kstore,
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
		"writeb":   Writeb,
		"writec":   Writec,
		"flush":    Flush,
		"readc":    Readc,
		"exit":     Exit,
	}

	// Maps from instruction -> string (built from strToInstrMap)
	instrToStrMap map[Bytecode]string
)

func NewInstruction(code Bytecode, arg uint32, data uint16) Instruction {
	return Instruction{
		code: uint32(code) | (uint32(data) << 8),
		arg:  arg,
	}
}

// Splits an instruction code into (bytecode, data) pair
func (instr Instruction) DecodeInstruction() (Bytecode, uint32) {
	return Bytecode(instr.code & 0xff), (instr.code & 0xffffff00) >> 8
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
	return b == Const || b == Byte || b == Load || b == Store || b == Kstore
}

// True if the bytecode can optionally accept an argument instead of
// always inspecting the stack
func (b Bytecode) OptionalOpArg() bool {
	return b == Addi || b == Addf || b == Muli || b == Mulf ||
		b == And || b == Or || b == Xor ||
		b == Push || b == Pop ||
		b == Jmp || b == Jz || b == Jnz || b == Jle || b == Jl || b == Jge || b == Jg
}

func (instr Instruction) String() string {
	code, data := instr.DecodeInstruction()
	if code.RequiresOpArg() || (code.OptionalOpArg() && data > 0) {
		intArg := int32(instr.arg)
		if intArg < 0 {
			// Add both the negative and unsigned version to the output
			return fmt.Sprintf("%s %d (%d)", code.String(), intArg, instr.arg)
		}
		// Only include the unsigned version
		return fmt.Sprintf("%s %d", code.String(), instr.arg)
	} else {
		// No op arg - only include code string
		return code.String()
	}
}

// This is called when package is first loaded (before main)
func init() {
	// Some kind of check like this will probably be needed if we eventually decide
	// to serialize instructions to file
	//
	// var instr Instruction
	// if unsafe.Sizeof(instr) != 8 {
	// 	panic("Instruction struct size not equal to 8")
	// }

	// Build instruction -> string map using the existing string -> instruction map
	instrToStrMap = make(map[Bytecode]string, len(strToInstrMap))
	for s, b := range strToInstrMap {
		instrToStrMap[b] = s
	}
}
