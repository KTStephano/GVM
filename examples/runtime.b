    // adds input reserved bytes and program size bytes
    addi
    rstore 2           // store in register[2] (tells us the beginning of unused heap)

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

    const __writeBytes
    const 0xA8
    storep32

    // Move into non-privileged mode
    const 1
    srstore 32          // special reserved register 32 (CPU mode)

    // Call main program code
    call main

    // If we get here, call exit to quit
    call exit

// stack[0] -> return address
// read 1 character from stdin (return value is in register[2])
readc: 
    sysint 0xA0         // make a system call to get the next character (result is stored in register[2])
    return              

// 0xA0
__requestCharInput:
    const 0
    rstore 2            // clear register[2] (will hold char return value)

    // set up a character input request from console IO device
    const 0             // no input data
    const 0             // unused interaction id
    write 3 4           // port 3 = console IO device, command 4 = read 32-bit character
    pop 4               // get rid of write result
    
    // At some point while spinning we will be interrupted to run __handleCharInput
__waitForChar:
    rload 2
    jz __waitForChar    // busy wait loop - could be replaced with context switch to other task in future
    resume              // register[2] no longer 0 - resume caller

// 0x0C
__handleCharInput:
    pop 8               // get rid of the interaction id and byte count
    rstore 2            // store 32-bit character in register 2
    resume

// stack[0] -> return address
// stack[1] -> frame pointer
// stack[2] -> 0-terminated string
// register[2] will contain string length in bytes (not including 0-byte)
strlen:
    rload 3              // load register[3] so we can restore it later
    
    rload 1              // load stack pointer
    addi 12              // skip past register[3], ret addr and frame pointer to stack pointer where string address sits
    loadp32              // *stack[0] to get string address
    rstore 3             // place string address into register[3]

    const 0             
    rkstore 2            // set up accumulator register, keep value on stack

__strlenLoop:
    rload 3              // load string address
    addi                 // add counter to address
    loadp8               // load byte from string address
    jz __strlenDone      // if byte is zero, jump to __strlenDone
    raddi 2 1            // increment acumulator register, store updated value on stack
    jmp __strlenLoop

__strlenDone:
    rstore 3             // restore value of register[3]
    return

// stack[0] -> return address
// stack[1] -> frame pointer
// stack[2] -> 0-terminated string address
print:
    rload 3              // load value of register[3] so we can restore it later
    rload 1              // load stack pointer
    addi 12              // skip past value of register[3], ret addr and frame pointer
    loadp32              // *stack[0] to place string address on top of stack for argument to strlen
    rkstore 3            // store string address in register[3], keep address on stack
    call strlen
    sysint 0xA8          // system call to __writeBytes
    pop 4                // remove trailing string address from stack
    rstore 3             // restore value of register[3]
    return

// 0xA8
// register[2] should have string length, register[3] should have string address
__writeBytes:
    rload 3              // load string address
    rload 2              // load string length
    const 8              // 8 bytes of input
    const 0              // unused interaction id
    write 3 3            // port 3 = console IO device, command 3 = write N bytes from address
    resume

exit:
    sysint 0xA4

// 0xA4
__requestExit:
    const 0             // unused data
    const 0             // unused interaction id
    write 1 3           // port 1 = power controller, command 3 = perform poweroff
    halt                // stops CPU here in case power device takes a bit to shutdown