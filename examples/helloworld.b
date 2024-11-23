    byte 0
    const "Hello world!\n"
    load 1                      // load stack pointer
    kstore 2                    // store address in register 2, keep address on stack
loop:
    loadp8                      // *stack[0] 8 bit read, widen to 32 bits
    jnz writechar               // if current character is not 0, jump to writechar
    flush
    exit                        // current character was 0 - flush output and quit
writechar:
    load 2
    loadp8                      // *stack[0] 8 bit read, widen to 32 bits
    writec                      // write 32 bit value to stdout
    load 2
    addi 1                      // add 1 to string address
    kstore 2                    // store new address in register[2], keep address on stack
    jmp loop                    // unconditional jump to loop