    const 50000000
    rstore 2            // loads count into register[2]

loop:
    raddi 2 -1          // register[2] += -1 and pushes result to stack
    jnz loop
    const 0             // no data required
    const 0             // interation id unused
    write 1 3           // port: 1 (power management unit)
                        // cmd:  3 (perform poweroff)