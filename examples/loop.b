    const 50000000
    store 2

loop:
    const -1
    load 2
    addi             // addi register[2] -1
    store 2
    load 2
    jnz loop
    exit