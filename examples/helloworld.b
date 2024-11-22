    byte 0
    const "Hello, world!\n"     // Push string onto stack with trailing 0 byte
    load 1                      // Load stack top address which contains the start of our string
    const done                  // Address we want print function to return to
    jmp print

done:
    exit