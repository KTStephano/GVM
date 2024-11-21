    byte 0
    const "Hello, world!\n"
    load 1                      // load stack pointer
    store 2                     // store address in register 2

loop:
    load 2                      // load current address
    const -1
    addi
    store 2                     // subtract 1 byte from string address, store in register 2
    load 2                      // load new address back onto stack
    loadp8                      // dereference 1 byte, widen to 32 bits
    const done
    jz                          // if current byte is 0, jump to done
    load 2
    loadp8
    writec                      // load current character back to stack, write to console
    const loop
    jmp                         // unconditional jump back to loop

done:
    exit