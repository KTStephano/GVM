    const handleTimerInterrupt
    const 0
    storep32

    const 1000000       // microseconds
    const 4             // num bytes of data (we only write 32-bit microsecond value)
    const 123           // interaction ID
    write 0 2           // write <port> <command> - port 0 is the timer interrupt device
    pop 4               // remove result of write from stack

    const 1000000
    rstore 2            // loads count into register[2]

loop:
    raddi 2 -1          // register[2] += -1 and pushes result to stack
    jnz loop
    halt

    // called when timer goes off and interrupts loop if it hasn't completed
    //
    // args:
    //      id,
    //      numBytes (will be 0)
    //      sp (used by resume)
    //      pc (used by resume)
handleTimerInterrupt:
    pop 8               // removes the id and num bytes args from stack

    // Set the timer interrupt to point to the halting variant
    const handleTimerInterruptHalt
    const 0
    storep32

    // Set up a new timer
    const 1000000       // microseconds
    const 4             // num bytes of data (we only write 32-bit microsecond value)
    const 123           // interaction ID
    write 0 2           // write <port> <command> - port 0 is the timer interrupt device
    pop 4               // removes result of write from the stack

    resume

handleTimerInterruptHalt:
    halt