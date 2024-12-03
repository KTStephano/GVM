    // adds input reserved bytes and program size bytes
    addi
    rkstore 3          // store in register[2] (tells us the beginning of unused heap), keep value on stack
    srstore 33         // store beginning of unused heap in special reserved register 33

    // Set up memory bounds
    rload 1            // load stack pointer (max heap address)
    rload 3            // load end of program+reserved segment (min heap address)
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

// fp[0] -> return address
// fp[4] -> old frame pointer
// read 1 character from stdin and store result on stack
readc:
    const 0             // zeroed 4 bytes for return value
    sysint 0xA0         // make a system call to get the next character (result is stored in register[3])
    return 4            // return top 4 bytes on stack

// 0xA0
//
// fp[0] -> old PC
// fp[4] -> old SP
// fp[8] -> old FP
// fp[12] -> old mode
// fp[16] -> beginning of 4 byte buffer to store result
__requestCharInput:
    rload 2             // load fp
    addi 16             // address of return buffer at offset 16 bytes
    srload 33           // load register[33] which contains beginning of unused heap
    storep32            // place return buffer address at beginning of unused heap

    // set up a character input request from console IO device
    const 0             // no input data
    const 0             // unused interaction id
    write 3 4           // port 3 = console IO device, command 4 = read 32-bit character
    
    // At some point while spinning we will be interrupted to run __handleCharInput
__waitForChar:
    rload 2
    loadp32 16          // perform *(fp+16)
    jz __waitForChar    // busy wait loop - could be replaced with context switch to other task in future
    resume              // data buffer no longer 0 - resume caller

// 0x0C
//
// stack[0] -> interaction id (fp[-12])
// stack[4] -> number of bytes of input character (4) (fp[-8])
// stack[8] -> beginning of input character (fp[-4])
// fp[0] -> old PC
// fp[4] -> old SP
// fp[8] -> old FP
// fp[12] -> old mode
__handleCharInput:
    pop 8               // get rid of the interaction id and byte count (byte count is 4 for this function)
    srload 33           // load buffer address in special register 33
    loadp32             // get buffer address
    storep32            // store 32-bit input character in return buffer
    resume

// fp[0] -> return address
// fp[4] -> old frame pointer
// fp[8] -> 0-terminated string address
// stack will contain return value
strlen:
    rload 3              // fp-4
    rload 4              // fp-8; load register[3, 4] so we can restore them later
    
    rload 2              // load curr frame pointer
    loadp32 8            // skip past ret addr and frame pointer to stack pointer where string address sits
    rstore 3             // place string address into register[3]

    const 0
    rkstore 4            // set up accumulator in register[4], keep value on stack

__strlenloop:
    rload 3           
    addi                 // add string address to current accumulator value
    loadp8
    jz __strlendone      // if current byte is 0 we're done
    raddi 4 1            // increment register[4] by 1, store new value on stack
    jmp __strlenloop

__strlendone:
    rload 4              // load value of counter

    rload 2              // load frame pointer
    loadp32 -4           // load pointer at offset -4 bytes
    rstore 3             // restore value of register[3]

    rload 2
    loadp32 -8           // load pointer at offset -8 bytes
    rstore 4             // restore value of register[4]

    return 4             // return top 4 bytes on stack

// fp[0] -> return address
// fp[4] -> frame pointer
// fp[8] -> 0-terminated string address
print:
    rload 2              // load frame pointer
    loadp32 8            // skip past value of ret addr and frame pointer to get string address
    call strlen
    sysint 0xA8          // system call to __writeBytes
    return

// 0xA8
//
// fp[0] -> old PC
// fp[4] -> old SP
// fp[8] -> old FP
// fp[12] -> old mode
// fp[16] -> num bytes to write
// fp[20] -> beginning of string address
__writeBytes:
    rload 2              // load frame pointer
    loadp32 20           // get string address
    rload 2              // load frame pointer
    loadp32 16           // get number of bytes to write
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