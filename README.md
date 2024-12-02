<img src="GVMIcon.png" width="512">

# Virtual Machine (written in Go)

This is an implementation of a custom virtual machine with its own (bytecode) instruction set. It is a hybrid stack and register based machine with support for interrupts, segmented heap and CPU privilege mode configuration.

# Running the examples

(Please feel free to reach out or open an issue if you discover any VM bugs!)

After compiling the gvm with `go build .`, you can run the examples as follows:
- ./gvm examples/runtime.b examples/helloworld.b
- ./gvm examples/runtime.b examples/input.b
- ./gvm examples/runtime.b examples/loop.b
- ./gvm examples/poweroff.b

The GVM executable accepts a -debug flag as well for starting the program in debug mode. This mode supports single stepping through instructions, setting breakpoints and printing the final assembled program.

# Specification

### vCPU
- Single core, 32-bit virtual architecture with little endian byte ordering
- Supports a set of hardware/software interrupts totaling 64
- 32 registers starting at index 0
- - register 0 is the program counter
- - register 1 is the stack pointer
- - registers indexed 2 through 31 are general purpose
- 8 specialized registers (sr/srs) indexed after the last general purpose register
- - sr 32 is the CPU "mode" - 0 means unprivileged, 1 means privileged
- - sr 33 is the frame counter used for `return`/`resume` instructions
- - srs indexed 34-39 are currently unused
- Supports single stepping through instructions in VM debug mode
- Supports setting program breakpoints in VM debug mode

### vRAM
- Minimum of 65KB total memory (shared with interrupt addresses and process instructions)
- Stack grows down from max address -> min address
- Segmentation of heap when in non-privileged mode is possible by interfacing with memory controller device

### vDevices
- Supports 16 virtual devices
- Each communicates with the CPU asynchronously via response bus
- Currently first 4 device slots are occupied
- - Slot/Port 0 (handler address 0x00) is the system timer
- - Slot/Port 1 (handler address 0x04) is the power controller
- - Slot/Port 2 (handler address 0x08) is the memory management unit
- - Slot/Port 3 (handler address 0x0C) is the console IO

### Hybrid stack/register design

The majority of all instructions modify the stack in some way. They either push values to the stack, pop values from the stack, or both. However, a few instructions deal directly with modifying registers to enable storing temporary working values.

Since this virtual machine is mainly intended to be a software implementation that relies on instruction interpretation, going with a hybrid design was an attempt to strike a balance between the performance of a virtual register design and the simplicity of a virtual stack design. For more information on the performance tradeoffs in software VMs, [see this paper](https://static.usenix.org/events/vee05/full_papers/p153-yunhe.pdf).

### Instruction Set

Each bytecode occupies 1 byte, and when compiled into a machine instruction it is represented using 2 bytes. The lowest byte is the bytecode and the highest byte is the number of arguments (always 0, 1 or 2 arguments).

After the 2 instruction bytes, there are another 2 bytes that represent the `register argument` for register ops.

The final 4 bytes hold the 32-bit argument for the instruction (if needed).

In the following table, `<>` means the argument is required while `[]` means the argument is optional. For optional arguments, that generally means that if they are not supplied then the VM will get the arguments from the stack dynamically. For example:

```assembly
    push
```
will pop stack[0] and use that as the byte count, whereas

```assembly
    push 81
```
will use 81 as the byte count

The arithmetic and logical functions work similarly with their optional arguments. The optional argument determines whether the operation pulls 2 values from the stack, or just 1.

```assembly
    const 5
    const 3
    addi
```
will add 3+5 and push the result to the stack, whereas

```assembly
    const 3
    addi 5
```
will add 3+5 and push the result to the stack, but it used 1 less instruction and stack argument.

| Bytecode | Args | Description |
| --- | --- | --- |
| nop | | No operation |
| byte | `<constant>` | Pushes a 1-byte const value onto the stack |
| const | `<constant>` | Pushes a 4-byte const value onto the stack |
| rload | `<register>` | Loads value of register onto the stack |
| rstore | `<register>` | Stores value of stack[0] into register and pops stack |
| rkstore | `<register>` | Stores value of stack[0] into register and leaves stack unchanged |
| loadp8, loadp16, loadp32 | | Loads 8-, 16-, or 32-bit value from address at stack[0] onto stack (essentially stack[0] = *stack[0]) |
| storep8, storep16, storep32 | | Narrows stack[1] to 8-, 16-, or 32-bits and writes it to address at stack[0] (essentially *stack[0] = cast(stack[1])) |
| push | `[constant]` | Reserve constant bytes on the stack |
| pop | `[constant]` | Free bytes back to the stack |
| addi, addf | `[constant]` | int and float add of either stack[0]+stack[1], or stack[0]+constant |
| subi, subf | `[constant]` | int and float subtraction |
| muli, mulf | `[constant]` | int and float multiplication |
| divi, divf | `[constant]` | int and float division |
| remu, rems | `[constant]` | unsigned and signed remainder after integer division of stack[0]/stack[1], or stack[0]/constant |
| remf | `[constant]` | Remainder after floating point division of stack[0]/stack[1], or stack[0]/constant |
| not | | Inverts all bits of stack[0] |
| and | `[constant]` | Logical AND between stack[0] and stack[1]/constant |
| or | `[constant]` | Logical OR between stack[0] and stack[1]/constant |
| xor | `[constant]` | Logical XOR between stack[0] and stack[1]/constant |
| shiftl | `[constant]` | Shift stack[0] left by stack[1]/constant |
| shiftr | `[constant]` | Shift stack[0] right by stack[1]/constant |
| jmp | `[constant]` | Unconditional jump to address at stack[0]/constant |
| jz | `[constant]` | Jump to address at stack[0]/constant if stack[1] (or stack[0] if constant is supplied) is 0 |
| jnz | `[constant]` | Jump to address at stack[0]/constant if stack[1] (or stack[0] if constant is supplied) is not 0 |
| jle | `[constant]` | Jump to address at stack[0]/constant if stack[1] (or stack[0] if constant is supplied) is <= 0 |
| jl | `[constant]` | Jump to address at stack[0]/constant if stack[1] (or stack[0] if constant is supplied) is < 0 |
| jge | `[constant]` | Jump to address at stack[0]/constant if stack[1] (or stack[0] if constant is supplied) is >= 0 |
| jg | `[constant]` | Jump to address at stack[0]/constant if stack[1] (or stack[0] if constant is supplied) is > 0 |
| call | `[address]` | Push next program address to the stack and jump either to [address] or stack[0] |
| return | | Clear stack back to beginning of current stack frame and return to caller |
| resume | | Similar to return, but for resuming previous execution after interrupt handler is done |
| sysint | `<address>` | Invokes a privileged interrupt handler at <address> |
| raddi, raddf | `<register> [constant]` | Add register to stack[0]/constant, update register and push new result to stack |
| rsubi, rsubf | `<register> [constant]` | Subtract register from stack[0]/constant, update register and push new result to stack  |
| rmuli, rmulf | `<register> [constant]` | Multiply register with stack[0]/constant, update register and push new result to stack  |
| rdivi, rdivf | `<register> [constant]` | Divide register and stack[0]/constant, update register and push new result to stack  |
| rshiftl | `<register> [constant]` | Register shift left by stack[0]/constant, update register and push new result to stack |
| rshiftr | `<register> [constant]` | Register shift right by stack[0]/constant, update register and push new result to stack |
| cmpu, cmps | | unsigned and signed comparison between stack[0] and stack[1]: -1 if stack[0] < stack[1], 0 if stack[0] == stack[1] and 1 if stack[0] > stack[1] |
| cmpf | | floating point comparison between stack[0] and stack[1]: -1 if stack[0] < stack[1], 0 if stack[0] == stack[1] and 1 if stack[0] > stack[1] |
| write | `<port> <command>` | Performs a device write to request device at port perform some command (see below) |
| halt | | Puts CPU into "waiting for next instruction" state, which is interruptible |

# Interfacing with vDevices

`write <port> <command>` is the primary way for privileged instructions to communicate with the different virtual devices connected to the CPU.
- if `command` = 0, performs get hardware device info (no arguments on stack are needed)
- - when this completes the stack will contain:
- - - stack[0] = HWID
- - - stack[1] = num metadata bytes (can be 0)
- - - stac[2]+ = metadata bytes

- if `command` = 1, performs get hardware device status (stack should contain 2 constant arguments of 0, 0 which means unused interaction id and no bytes as input)
- - when this completes it will push a 32-bit status code to the stack:
- - - 0x00 = device not found
- - - 0x01 = device ready (write req would succeed)
- - - 0x02 = device busy (write req would fail)

- otherwise performs a device-specific operation
- - input stack[0] should be the interaction id (for identifying request when response comes in)
- - input stack[1] should be the number of bytes to write
- - input stack[2] should be the start of the data to write
- - when this completes the stack will contain a status code the same as if command = 1 (see above)

### Interfacing examples

```assembly
    write 3 0       // gets device info for device #3

    const 0         // no bytes
    const 0         // unused interaction id
    write 3 1       // gets device status for device #3

    const 's'
    const 4         // 4 byte input ('s')
    const 0         // unused interaction id
    write 3 2       // tells device 3 (console IO) to write a single character

    const 0         // no bytes
    const 123       // interaction id of 123 (32 bits)
    write 3 4       // tells device 3 (console IO) to read a single 32-bit character
```

### Device list with their ports/addresses and commands

#### -> port 0 (handler address 0x00) is system timer
- - command 2 is "set new timer"
- - - expects 4 byte input representing microseconds

#### -> port 1 (handler address 0x04) is power controller
- command 2 is "perform restart"
- - expects no inputs
- command 3 is "perform poweroff"
- - expects no inputs

#### -> port 2 (handler address 0x08) is memory management unit
- command 2 is "set new min/max heap addr bounds" (only applies to non-privileged code)
- - expects 8 bytes of input
- - - first 4 bytes: min heap address
- - - next 4 bytes: max heap address
- command 3 is "update to previously set min/max based on privilege level"
- - expects no input
- - if CPU mode is 0 (max privilege), unlocks entire memory address range
- - if CPU mode is not 0 (non-privileged mode), resets to previous min/max heap addresses

#### -> port 3 (handler address 0x0C) is console IO
- command 2 is "write a single 32-bit character"
- - expects 4 byte input
- command 3 is "write N bytes from address"
- - expects 8 byte input
- - - first 4 bytes: number of bytes to write
- - - next 4 bytes: address to start reading bytes from
- command 4 is "read 32-bit character"
- - expects no input
- - when data comes in, it is forwarded to handler address 0x0C

#### -> ports 4-15 are currently unused