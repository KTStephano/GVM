    const 70000000
    store 2
loop:
    load 2
    const -1
    addi
    store 2
    load 2
    const loop
    jnz
    exit