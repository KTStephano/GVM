package gvm

/*
	For each CPU core:
			- little endian
			- 32-bit virtual architecture
			- 32 registers starting at index 0
			- register 0 is the program counter
			- register 1 is the stack pointer
			- registers indexed 2 through 31 are general purpose, 32-bit
			- 8 specialized registers (sr/srs)
			- sr 0 is the CPU "mode" - 0 means unprivileged, 1 means privileged
			- sr 1 is the memory segment start (when in unprivileged mode)
			- sr 2 is the memory segment end (when in unprivileged mode)
			- srs 3-5 are reserved
			- srs 6-7 can be used for anything
			- supports single stepping through instructions
			- supports setting program breakpoints

	The memory segment is 64kb in size minimum
		- bytes 0-255 are reserved for the interrupt vector table (IVT)
		- startup program starts at byte 256
		- by default, entire memory segment is read/write at startup

	Devices
		- There are 16 device slots (indexed 0-15 when using write instruction)
		- write instruction accepts a command:
			-> 0 means "get device info"
			-> 1 means "get device status"
			-> 2+ are device specific
		- port 0 (handler address 0x00) is system timer
			-> command 2 is "set new timer"
				-> expects 4 byte input representing microseconds
		- port 1 (handler address 0x04) is power controller
			-> command 2 is "perform restart"
				-> expects no inputs
			-> command 3 is "perform poweroff"
				-> expects no inputs
		- port 2 (handler address 0x08) is memory management unit
			-> command 2 is "set new min/max heap addr bounds" (only applies to non-privileged code)
				-> expects 8 byte input
					-> first 4 bytes: min heap address
					-> next 4 bytes: max heap address
			-> command 3 is "update to previously set min/max based on privilege level"
				-> expects no input
				-> if CPU mode is 0 (max privilege), unlocks entire memory address range
				-> if CPU mode is not 0 (non-privileged mode), resets to previous min/max heap addresses
		- port 3 (handler address 0x0C) is console IO
			-> command 2 is "write a single 32-bit character"
				-> expects 4 byte input
			-> command 3 is "write N bytes from address"
				-> expects 8 byte input
					-> first 4 bytes: number of bytes to write
					-> next 4 bytes: address to start reading bytes from
			-> command 4 is "read 32-bit character"
				-> expects no input
				-> when data comes in, it is forwarded to handler address 0x0C
		- ports 4-15 are currently unused

	Exceptions
		- There are 5 exceptions that can be caught and handled by the code
		- segmentation fault (handler address 0x40)
		- division by zero (handler address 0x44)
		- unknown instruction (handler address 0x48)
		- illegal instruction (handler address 0x76)
		- IO error (handler address 0x50)
		- [0x54, 0xA0) are currently unused

	Public interrupts
		- Address range [0xA0, 0x100) can be called from public code and are unspecified what they are for

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

			rload   <register> (loads value of register)
			rstore  <register> (stores value of stack[0] to register)
			rkstore <register> (stores value of stack[0] to register and keeps value on the stack)

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

		Function control flow

			call [address] (push next program address to the stack and jump either to [address] or stack[0])
			return 		   (compiles down to unconditional jump)

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

			write <port> <command> (port is a device index from 0-15)
				-> if command = 0, performs get hardware device info
					when this completes the stack will contain:
						-> stack[0] = HWID
						-> stack[1] = num metadata bytes (can be 0)
						-> stac[2]+ = metadata bytes

				-> if command = 1, performs get hardware device status
					when this completes it will push a 32-bit status code to the stack:
						-> 0x00 = device not found
						-> 0x01 = device ready (write req would succeed)
						-> 0x02 = device busy (write req would fail)

				-> otherwise
					input stack[0] should be the interaction id (for identifying request when response comes in)
					input stack[1] should be the number of bytes to write
					input stack[2] should be the start of the data to write

					when this completes the stack will contain a status code the same as if command = 1 (see above)

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

	Byte    Bytecode = 0x01
	Const   Bytecode = 0x02
	Rload   Bytecode = 0x03
	Rstore  Bytecode = 0x04
	Rkstore Bytecode = 0x05

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
	Call Bytecode = 0x4A

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

	Sysint Bytecode = 0x70
	Resume Bytecode = 0x71

	Write Bytecode = 0xF1

	Srload  Bytecode = 0xF2
	Srstore Bytecode = 0xF3

	Halt Bytecode = 0xFF
)

var (
	// Maps from string -> instruction
	strToInstrMap = map[string]Bytecode{
		"nop":      Nop,
		"byte":     Byte,
		"const":    Const,
		"rload":    Rload,
		"rstore":   Rstore,
		"rkstore":  Rkstore,
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
		"call":     Call,
		"return":   Jmp, // compiles to an unconditional jump
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
		"sysint":   Sysint,
		"resume":   Resume,
		"write":    Write,
		"srload":   Srload,
		"srstore":  Srstore,
		"halt":     Halt,
	}

	// Maps from instruction -> string (built from strToInstrMap)
	instrToStrMap map[Bytecode]string
)

// Convert bytecode to string for use with Print/Sprint
func (b Bytecode) String() string {
	str, ok := instrToStrMap[b]
	if !ok {
		str = "?unknown?"
	}
	return str
}

// True if the bytecode deals with register load/store/arithmetic/logic
func (b Bytecode) IsRegisterOp() bool {
	return b == Rload || b.IsRegisterWriteOp()
}

func (b Bytecode) IsPrivilegedRegisterOp() bool {
	return b == Srload || b == Srstore
}

// Returns true for all instructions that write to a register
func (b Bytecode) IsRegisterWriteOp() bool {
	return b == Rstore || b == Rkstore || b.IsRegisterReadWriteOp()
}

// Returns true for all instructions that both read and write to a register
func (b Bytecode) IsRegisterReadWriteOp() bool {
	return b == Raddi || b == Raddf || b == Rsubi || b == Rsubf || b == Rmuli || b == Rmulf || b == Rdivi || b == Rdivf ||
		b == Rshiftl || b == Rshiftr
}

// Returns true if the instruction deals with hardware device interfacing
func (b Bytecode) IsHardwareDeviceOp() bool {
	return b == Write
}

// True if the bytecode requires an argument to be paired with it, such as const X
func (b Bytecode) NumRequiredOpArgs() int {
	if b == Const || b == Byte || b.IsRegisterOp() || b == Srload || b == Srstore || b == Sysint {
		return 1
	} else if b.IsHardwareDeviceOp() {
		return 2
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
		b == Call ||
		b.IsRegisterReadWriteOp() {
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
