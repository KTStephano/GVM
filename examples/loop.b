    const handle
    const 0
    storep32

    const 1000000       // microseconds
    const 4             // num bytes of data (we only write 32-bit microsecond value)
    const 123           // interaction ID
    write 0 2           // write <port> <command> - port 0 is the timer interrupt device

    const 1000000000
    rstore 2            // loads count into register[2]

loop:
    raddi 2 -1          // register[2] += -1 and pushes result to stack
    jnz loop
    halt

    // called when timer goes off and interrupts loop if it hasn't completed
handle:
    const 23423523
    halt