    const 50000000
    store 2             // Loads count into register[2]

loop:
    load 2
    addi -1             // addi register[2] -1
    kstore 2            // Loads updated count into register[2] and keeps it on the stack
    jnz loop
    exit