    const 9
    store 2      // store 9 in register[2]

loop:
    load 2
    addi '0'     // add register[2] to '0' character to get number as character
    writec
    const 1
    load 2
    subi
    store 2      // subtract 1 from register[2] and store result back in register[2]
    load 2
    jge loop     // if register[2] >= 0, jump to loop
    const '\n'
    writec       // add trailing newline
    flush        // flush stdout to console
    exit