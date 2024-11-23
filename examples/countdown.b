    const 9
    store 2      // Store 9 in register[2]

loop:
    load 2
    addi '0'    // Add register[2] to '0' character to get number as character
    writec
    load 2
    subi 1
    store 2     // Subtract 1 from register[2] and store result back in register[2]
    load 2
    jge loop    // If register[2] >= 0, jump to loop
    const '\n'
    writec      // Add trailing newline
    exit