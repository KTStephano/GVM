main:    
    // loop 50M times (just under half a second on Apple M1)
    const 50000000
    rstore 3            // loads count into register[3]

loop:
    raddi 3 -1          // register[3] += -1 and pushes result to stack
    jnz loop
    return