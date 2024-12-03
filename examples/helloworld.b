main:
    byte 0                   // 0 terminate the string
    const "Hello world!\n"
    rload 1                  // load stack pointer
    call fmt.Print
    return