    load 1
    store 3                 // store start stack pointer in register[3]
    const "Hello world!\n"  // store string on stack
    load 1
    store 2                 // store new stack pointer in register[2]

loop:
    load 2                  // load current string address
    loadp8                  // *stack[0] 1 byte read
    writec
    load 3
    raddi 2 1               // register[2] += 1, store result address on stack
    cmpu                    // compare new address in register[2] with end address in register[3]
    jl loop                 // if curr address still less than end address, go to loop
    flush
    exit

