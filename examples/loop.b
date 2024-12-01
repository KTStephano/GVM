main:    
    const 50000000
    rstore 2            // loads count into register[2]

loop:
    raddi 2 -1          // register[2] += -1 and pushes result to stack
    jnz loop
    jmp                 // return to caller (runtime)