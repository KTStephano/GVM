main:
    rload 1
    byte 0                   // 0 terminate the string
    const "Hello world!"
    rload 1                  // load stack pointer
    call print
    return