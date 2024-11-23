    byte 0
    const "Hello, world!\n"
    load 1                      // load stack pointer
    store 2                     // store address in register 2

loop:
    load 2                      // load string address from register 2
    loadp8                      // dereference 1 byte (*stack[0]), widen to 32 bits
    jnz writechar               // if character is not 0, jump to writechar
    exit
writechar:
    load 2
    loadp8                      // *stack[0] (dereference)
    writec                      // write 32 bit character
    load 2
    const 1
    addi
    store 2                     // addi register[2] 1, store in register[2]
    jmp loop