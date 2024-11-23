    const 50000000
    store 2

loop:
    load 2
    addi -1             // addi register[2] -1
    store 2
    load 2
    jnz loop
    exit