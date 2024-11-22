    byte 0
    const "Hello, world!\n"     // Push string onto stack with trailing 0 byte
    load 1                      // Load stack top address which contains the start of our string
    const done                  // Address we want print function to return to
    jmp print

done:
    exit

// Print function
// stack[0] should be return address
// stack[1] should be address of beginning of string (0 terminated)
print:
    load 2              // Load current value of register[2] so we can restore it later
                        // stack[0]: register[2], stack[1]: return address, stack[2]: string address
    load 1              // Load stack top address
    const 8
    addi                // Adds 8 bytes to stack top address, store resulting address on stack
                        // First 4 bytes: skip past old value of register[2]
                        // Next 4 bytes: skip past return address
    loadp32             // *stack[0] to get the value it contains (which is the string start address)                
    store 2             // Store string start address in register[2]
__printloop:
    load 2
    loadp8              // *stack[0], widens to 32 bits
    jnz __writechar     // if current character isn't 0, jump to __writechar
    store 2             // Pop original value of register[2] off the stack and store it back in register[2]
    jmp                 // Unconditional jump to return address on stack
__writechar:
    load 2
    loadp8              // *stack[0], widens to 32 bits
    writec
    load 2
    const 1
    addi
    store 2             // Add 1 to the string address, store new address back in register[2]
    jmp __printloop