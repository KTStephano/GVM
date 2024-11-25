package gvm

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

	This bytecode instruction set attempts to strike a balance between the extreme simplicity
	of a stack-based design and the increased complexity but better performance (at least for interpreter VMs) of
	a register-based design. Almost all instructions revolve around the stack, but there are some additions
	to reduce the number of instructions required in some situations. For example:
		load 2
		const 1
		addi
		store 2

	can become
		load 2
		addi 1
		store 2

	where the const instruction is removed in favor of inlining the argument with addi.

	See https://static.usenix.org/events/vee05/full_papers/p153-yunhe.pdf
	for a look at virtual register vs virtual stack designs for interpreter VMs.

	Current bytecodes (<> means required, [] means optional)
			nop    (no operation)
			byte   <constant> (pushes byte value onto the stack)
			const  <constant> (pushes const value onto stack (can be a label))

			load   <register> (loads value of register)
			store  <register> (stores value of stack[0] to register)
			kstore <register> (stores value of stack[0] to register and keeps value on the stack)

			loadp8, loadp16, loadp32 (loads 8, 16 or 32 bit value from address at stack[0], widens to 32 bits)
				loadpX are essentially stack[0] = *stack[0]
			storep8, storep16, storep32 (narrows stack[1] to 8, 16 or 32 bits and writes it to address at stack[0])
				storepX are essentially *stack[0] = stack[1]

		The push/pop instructions accept an optional argument. This argument is the number of bytes to push to or pop from the stack.
		If no argument is specified, stack[0] should hold the bytes argument.

			push [constant] (reserve bytes on the stack, advances stack pointer)
			pop  [constant] (free bytes back to the stack, retracts stack pointer)

		All arithmetic instructions accept an optional argument. This is a fast path that will perform stack[0] <op> arg and overwrite
		the current stack value with the result.

			addi, addf [constant] (int and float add)
			subi, subf [constant] (int and float sub)
			muli, mulf [constant] (int and float mul)
			divi, divf [constant] (int and float div)

		The remainder functions work the same as % in languages such as C. It returns the remainder after dividing stack[0] and stack[1].
		There is a fast path for these as well that performs remainder stack[0] arg.

			remu, rems [constant] (unsigned and signed remainder after integer division)
			remf	   [constant] (remainder after floating point division)

		and, or, xor instructions all take an optional argument. This is a fast path that will perform stack[0] <op> arg and then overwrite
		the current stack value with the result.

			not 		   (inverts all bits of stack[0])
			and [constant] (logical AND between stack[0] and stack[1])
			or  [constant] (logical OR between stack[0] and stack[1])
			xor [constant] (logical XOR between stack[0] and stack[1])

			shiftl (shift stack[0] left by stack[1])
			shiftr (shift stack[0] right by stack[1])

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

		The r* style of instructions accept a register as their first argument. If no second argument is given,
		it performs registers[arg0] += stack[0] and overwrites the top stack value with the result. Otherwise it
		will perform registers[arg0] += arg1 and push the result to the stack. In both cases not only does the stack
		store the result, but the register is updated as well.

			raddi, raddf <register> [constant]
			rsubi, rsubf <register> [constant]
			rmuli, rmulf <register> [constant]
			rdivi, rdivf <register> [constant]

			rshiftl <register> [constant] (shift register left)
			rshiftr <register> [constant] (shift register right)

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

type Bytecode byte

const (
	Nop Bytecode = 0x00

	Byte   Bytecode = 0x01
	Const  Bytecode = 0x02
	Load   Bytecode = 0x03
	Store  Bytecode = 0x04
	Kstore Bytecode = 0x05

	Loadp8   Bytecode = 0x10
	Loadp16  Bytecode = 0x11
	Loadp32  Bytecode = 0x12
	Storep8  Bytecode = 0x13
	Storep16 Bytecode = 0x14
	Storep32 Bytecode = 0x15
	Push     Bytecode = 0x16
	Pop      Bytecode = 0x17

	Addi Bytecode = 0x20
	Addf Bytecode = 0x21
	Subi Bytecode = 0x22
	Subf Bytecode = 0x23
	Muli Bytecode = 0x24
	Mulf Bytecode = 0x25
	Divi Bytecode = 0x26
	Divf Bytecode = 0x27
	Remu Bytecode = 0x28
	Rems Bytecode = 0x29
	Remf Bytecode = 0x2A

	Not    Bytecode = 0x30
	And    Bytecode = 0x31
	Or     Bytecode = 0x32
	Xor    Bytecode = 0x33
	Shiftl Bytecode = 0x35
	Shiftr Bytecode = 0x34

	Jmp  Bytecode = 0x40
	Jz   Bytecode = 0x41
	Jnz  Bytecode = 0x42
	Jle  Bytecode = 0x43
	Jl   Bytecode = 0x44
	Jge  Bytecode = 0x45
	Jg   Bytecode = 0x46
	Cmpu Bytecode = 0x47
	Cmps Bytecode = 0x48
	Cmpf Bytecode = 0x49

	Writeb Bytecode = 0x50
	Writec Bytecode = 0x51
	Flush  Bytecode = 0x52
	Readc  Bytecode = 0x53

	Raddi   Bytecode = 0x60
	Raddf   Bytecode = 0x61
	Rsubi   Bytecode = 0x62
	Rsubf   Bytecode = 0x63
	Rmuli   Bytecode = 0x64
	Rmulf   Bytecode = 0x65
	Rdivi   Bytecode = 0x66
	Rdivf   Bytecode = 0x67
	Rshiftr Bytecode = 0x68
	Rshiftl Bytecode = 0x69

	Exit Bytecode = 0xFF
)

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
		"remu":     Remu,
		"rems":     Rems,
		"remf":     Remf,
		"not":      Not,
		"and":      And,
		"or":       Or,
		"Xor":      Xor,
		"shiftl":   Shiftl,
		"shiftr":   Shiftr,
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
		"raddi":    Raddi,
		"raddf":    Raddf,
		"rsubi":    Rsubi,
		"rsubf":    Rsubf,
		"rmuli":    Rmuli,
		"rmulf":    Rmulf,
		"rdivi":    Rdivi,
		"rdivf":    Rdivf,
		"rshiftl":  Rshiftl,
		"rshiftr":  Rshiftr,
		"exit":     Exit,
	}

	// Maps from instruction -> string (built from strToInstrMap)
	instrToStrMap map[Bytecode]string
)

// Convert bytecode to string
func (b Bytecode) String() string {
	str, ok := instrToStrMap[b]
	if !ok {
		str = "?unknown?"
	}
	return str
}

// True if the bytecode requires an argument to be paired with it, such as const X
func (b Bytecode) NumRequiredOpArgs() int {
	if b == Const || b == Byte || b == Load || b == Store || b == Kstore ||
		b == Raddi || b == Raddf || b == Rsubi || b == Rsubf || b == Rmuli || b == Rmulf || b == Rdivi || b == Rdivf ||
		b == Rshiftl || b == Rshiftr {
		return 1
	} else {
		return 0
	}
}

// True if the bytecode can optionally accept an argument instead of always inspecting the stack
func (b Bytecode) NumOptionalOpArgs() int {
	if b == Addi || b == Addf || b == Subi || b == Subf || b == Muli || b == Mulf || b == Divi || b == Divf ||
		b == Remu || b == Rems || b == Remf ||
		b == And || b == Or || b == Xor ||
		b == Shiftl || b == Shiftr ||
		b == Push || b == Pop ||
		b == Jmp || b == Jz || b == Jnz || b == Jle || b == Jl || b == Jge || b == Jg ||
		b == Raddi || b == Raddf || b == Rsubi || b == Rsubf || b == Rmuli || b == Rmulf || b == Rdivi || b == Rdivf ||
		b == Rshiftl || b == Rshiftr {
		return 1
	} else {
		return 0
	}
}

// This is called when package is first loaded (before main)
func init() {
	// Build instruction -> string map using the existing string -> instruction map
	instrToStrMap = make(map[Bytecode]string, len(strToInstrMap))
	for s, b := range strToInstrMap {
		instrToStrMap[b] = s
	}
}
