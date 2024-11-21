    byte 0
    const "Hello, world!\n"
    load 1                      // load stack pointer
    store 2                     // store address in register 2

loop: 
    load 2                      // load address in register 2
    const -1 
    addi                        // addi reg[2] -1
    store 2                     // store new address in register 2
    load 2                      // load address in register 2
    loadp8                      // dereference 1 byte from address, widen to 32 bits
    const done
    jz done                     // if character is 0, jump to done
    load 2                      // load address in register 2
    loadp8                      // dereference 1 byte, widen to 32 bits
    writec                      // write to console
    const loop
    jmp                         // unconditional jump to loop

done:
    exit