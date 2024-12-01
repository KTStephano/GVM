    const poweroff
    const 0
    storep32            // set timer interrupt handler to be poweroff

    // Set up a 1 second timer
    const 1000000       // microseconds
    const 4             // num bytes of data (we only write 32-bit microsecond value)
    const 123           // interaction ID
    write 0 2           // write <port> <command> - port 0 is the timer interrupt device
    pop 4               // remove result of write from stack

    halt                // puts the CPU into a waiting state (timer interrupt will break out of this)

poweroff:
    const 0             // no data required
    const 0             // interation id unused
    write 1 3           // port: 1 (power management unit)
                        // cmd:  3 (perform poweroff)