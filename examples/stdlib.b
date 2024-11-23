// Print function
// stack[0] should be return address
// stack[1] should be address of beginning of string (0 terminated)
print:
    load 2              // load current value of register[2] so we can restore it later
    load 1              // load stack top address
    addi 8              // adds 8 bytes to stack top address, store resulting address on stack
                        // first 4 bytes: skip past old value of register[2]
                        // next 4 bytes: skip past return address
    loadp32             // *stack[0] to get the value it contains (which is the start string address)
    kstore 2            // store string start address in register[2], keep address on stack
__printloop:
    loadp8              // *stack[0] 8 bit dereference, widens to 32 bits
    jnz __writebyte
    store 2             // comparison failed so pop original value of register[2] off the stack and store it back in register[2]
    flush               // flush buffered output to console
    jmp                 // unconditional jump to return address on stack
__writebyte:            // handles utf8 encoded strings by writing 1 byte at a time into stdout buffer
    load 2
    writeb              // writes 1 byte from string address we loaded from register[2]
    load 2
    addi 1              // add 1 to the string address
    kstore 2            // store new address back in register[2], keep address on stack
    jmp __printloop