    push 128        // reserve 128 bytes
    load 1          // load stack pointer
    store 2         // store in register 2
    const 0
    store 3         // store 0 (counter) in register 3
    byte 0
    const "Enter a line (less than 128 characters)\n"
    load 1
    const loop
    jmp print

loop:
    readc           // read a character
    kstore 4        // store it in register[4], keep value on stack
    const '\n'
    cmpu            // compare input character to newline
    jnz bufferadd   // if not equal to newline, jump to bufferadd
    const 0
    load 2
    load 3
    addi            // add address in register[2] to counter in register[3]
    storep8         // write (const 0) as byte to address at stack[0]
    load 2          // load string address
    const done      // instruction we want to jump to after print
    jmp print

done:
    const '\n'
    writec          // write trailing newline to stdout
    flush           // flush stdout to console 
    exit

bufferadd:
    load 4          // load character we stored in register[4]
    load 2
    load 3
    addi            // add address in register[2] to counter in register[3]
    storep8         // write character as byte to address at stack[0]
    load 3
    addi 1          // increment counter in register[3]
    store 3         // overwrite register[3] with new counter value
    jmp loop        // unconditional jump to loop