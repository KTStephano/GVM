    const 9
    store 2      // Store 9 in register[2]

loop:
    const '0'
    load 2
    addi        // Add register[2] to '0' character to get number as character
    writec
    const 1
    load 2
    subi
    store 2     // Subtract 1 from register[2] and store result back in register[2]
    load 2
    jge loop    // If register[2] >= 0, jump to loop
    const '\n'
    writec      // Add trailing newline
    exit