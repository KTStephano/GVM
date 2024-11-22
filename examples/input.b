    push 128        // Reserve 128 bytes
    load 1          // Load stack pointer
    store 2         // Store in register 2
    const 0
    store 3         // Store 0 (counter) in register 3
    byte 0
    const "Enter a line (less than 128 characters)\n"
    load 1
    const loop
    jmp print

loop:
    readc           // Read a character
    store 4         // Store it in register[4]
    load 4          // Restore register[4] to stack
    const '\n'
    cmpu            // Compare input character to newline
    jnz bufferadd   // If not equal to newline, jump to bufferadd
    const 0
    load 2
    load 3
    addi            // Add address in register[2] to counter in register[3]
    storep8         // Write (const 0) as byte to address at stack[0]
    load 2          // Load string address
    const done      // Instruction we want to jump to after print
    jmp print

done:
    const '\n'
    writec          // Print newline
    exit

bufferadd:
    load 4          // Load character we stored in register[4]
    load 2
    load 3
    addi            // Add address in register[2] to counter in register[3]
    storep8         // Write character as byte to address at stack[0]
    load 3
    const 1
    addi
    store 3         // Increment counter in register[3] and overwrite register[3]
                    // with new counter value
    jmp loop        // Unconditional jump to loop