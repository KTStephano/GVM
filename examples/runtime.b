    // adds input reserved bytes and program size bytes
    addi
    rstore 2           // store in register[2]

    // Set up memory bounds
    rload 1            // load stack pointer (max heap address)
    rload 2            // load end of program+reserved segment (min heap address)
    const 8            // 8 bytes of input to write
    const 0            // unused interaction id
    write 2 2          // set min/max memory bounds when in non-privileged mode
    pop 4              // remove result of write from stack (TODO: check status)

    // Set up interrupt handlers
    const __requestCharInput
    const 0xA0
    storep32

    const __handleCharInput
    const 0x0C
    storep32

    const __requestExit
    const 0xA4
    storep32

    // Move into non-privileged mode
    const 1
    srstore 32          // special reserved register 32 (CPU mode)

    // Call main program code
    call main

    // If we get here, call exit
    call exit

// read 1 character from stdin (return value is in register[2])
readc: 
    rload 3              // load value of registers 3/4/5 onto stack so we can restore it after
    rload 4
    rload 5

    sysint 0xA0         // make a system call to get the next character (result is stored in register[2])

    rstore 5
    rstore 4
    rstore 3            // restore registers 3/4/5
    jmp                 // jump back to caller

__requestCharInput:
    rstore 3             // store return address into register 3 (we're not returning directly from here)
    rstore 4             // store old SP into register 4
    rstore 5             // store old mode into register 5

    // set up a character input request from console IO device
    const 0             // no input data
    const 0             // unused interaction id
    write 3 4           // port 3 = console IO device, command 4 = read 32-bit character
    halt

__handleCharInput:
    pop 8               // get rid of the interaction id and byte count
    rstore 2            // store 32-bit character in register 2
    pop 12              // get rid of PC, SP and mode (pull from registers instead so as not to resume into a halt)
    
    rload 5             // old mode
    rload 4             // old SP
    rload 3             // old PC
    resume

exit:
    sysint 0xA4
    halt

__requestExit:
    const 0             // unused data
    const 0             // unused interaction id
    write 1 3           // port 1 = power controller, command 3 = perform poweroff
    resume