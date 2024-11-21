    // Push string onto the stack with trailing 0 byte
    byte 0
    
    // const <string> pushes the characters onto the stack in reverse order
    const "Hello, world!\n"

    // Load value of stack pointer and subtract 1 byte
    // Resulting address is start of our string and only argument to print
    sp
    const -1
    addi            // addi sp -1
    const finished  // finished is where we want print function to return to
    const print
    jmp             // jumps to address at stack[0]

finished:
    exit

// top of stack: return address (4-byte)
// next argument: address of string (4-byte)
print:
    sp
    const -8        
    addi            // addi sp -8
    loadp32         // dereference 4 byte (32 bit) value at address from stack[0]
loop:
    const 2
    store           // stores current string address in register[2]
    const 2
    load            // restores current string address onto stack
    loadp8          // dereference 1 byte (8 bits) value at address from stack[0], widens to 32-bits
    const writechar
    jnz
    const 4
    pop             // pop 4 bytes from stack to clear the const and character
    sp
    const -4
    addi            // addi sp -4 : this will hold the return address
    loadp32         // dereference 4 bytes (32 bits) value from address at stack[0]
    jmp             // jumps to return address at stack[0]
writechar:
    writec
    const -1
    const 2
    load
    addi            // addi register[2] -1
    const loop
    jmp