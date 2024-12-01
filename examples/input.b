main:
    byte 0                   // 0 terminate the string
    const "Please input a string and it will be echoed to the console:\n"
    rload 1                  // load stack pointer
    call print

    byte 0                   // this will serve as byte stop marker for readc
readloop:
    call readc
    rload 2                  // load result of readc which is stored in register[2]
    rload 1                  // load stack pointer
    call print              
    pop 4                    // remove 4-byte sp address
    const '\n'
    cmpu                     // compare unsigned input character to newline
    jz done                  // if equal to newline jump to done
    jmp readloop 

done:
    return