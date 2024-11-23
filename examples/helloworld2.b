    byte 0
    const "Hello world!\n"  // push string onto stack with trailing 0 byte
    load 1                  // load stack top address which contains the start of our string
    const done              // address we want print function to return to
    jmp print

done:
    exit