package gvm

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func getDefaultRecoverFuncForVM(vm *VM) func() {
	// Allows us to handle critical errors that came up during execuion
	return func() {
		if r := recover(); r != nil {
			err := errSegmentationFault
			if vm.errcode != nil {
				err = vm.errcode
			}

			fmt.Printf("%s%s\n", err, vm.formatInstructionStr(" at instruction:"))
		}
	}
}

func (vm *VM) execNextInstruction() {
	pc := vm.programCounter()
	if *pc >= Register(len(vm.program)) {
		vm.errcode = errProgramFinished
		return
	}

	instr := vm.program[*pc]
	*pc += 1

	switch instr.code {
	case Nop:
	case Sp:
		vm.pushStack(uint32(*vm.stackPointer()))
	case Byte:
		vm.pushStackByte(instr.arg)
	case Const:
		vm.pushStack(instr.arg)
	case Load:
		// read register index from stack
		stackTop := vm.peekStack(varchBytes)
		regIdx := uint32FromBytes(stackTop)
		// overwrite register index on stack with register value
		uint32ToBytes(uint32(vm.registers[regIdx]), stackTop)
	case Store:
		regIdx := uint32FromBytes(vm.popStack())
		if regIdx < 2 {
			// not allowed to write to program counter or stack pointer
			vm.errcode = errIllegalOperation
			return
		}

		regValue := uint32FromBytes(vm.popStack())
		vm.registers[regIdx] = Register(regValue)
	case Loadp8:
		loadpX(vm, 1)
	case Loadp16:
		loadpX(vm, 2)
	case Loadp32:
		loadpX(vm, 4)
	case Storep8:
		storepX(vm, 1)
	case Storep16:
		storepX(vm, 2)
	case Storep32:
		storepX(vm, 4)
	case Push:
		bytes := uint32FromBytes(vm.popStack())
		sp := vm.stackPointer()
		*sp = *sp + Register(bytes)
	case Pop:
		bytes := uint32FromBytes(vm.popStack())
		sp := vm.stackPointer()
		*sp = *sp - Register(bytes)
	case Addi:
		arithmeticLogical(vm, arithAddi)
	case Addf:
		arithmeticLogical(vm, arithAddf)
	case Subi:
		arithmeticLogical(vm, arithSubi)
	case Subf:
		arithmeticLogical(vm, arithSubf)
	case Muli:
		arithmeticLogical(vm, arithMuli)
	case Mulf:
		arithmeticLogical(vm, arithMulf)
	case Divi:
		arithmeticLogical(vm, arithDivi)
	case Divf:
		arithmeticLogical(vm, arithDivf)
	case Not:
		arg := vm.peekStack(varchBytes)
		// Invert all bits, store result in arg
		uint32ToBytes(^uint32FromBytes(arg), arg)
	case And:
		arithmeticLogical(vm, logicalAnd)
	case Or:
		arithmeticLogical(vm, logicalOr)
	case Xor:
		arithmeticLogical(vm, logicalXor)
	case Jmp:
		addr := uint32FromBytes(vm.popStack())
		*pc = Register(addr)
	case Jz:
		addr := uint32FromBytes(vm.popStack())
		value := uint32FromBytes(vm.peekStack(varchBytes))
		if value == 0 {
			*pc = Register(addr)
		}
	case Jnz:
		addr := uint32FromBytes(vm.popStack())
		value := uint32FromBytes(vm.peekStack(varchBytes))
		if value != 0 {
			*pc = Register(addr)
		}
	case Jle:
		addr := uint32FromBytes(vm.popStack())
		value := uint32FromBytes(vm.peekStack(varchBytes))
		if int32(value) <= 0 {
			*pc = Register(addr)
		}
	case Jl:
		addr := uint32FromBytes(vm.popStack())
		value := uint32FromBytes(vm.peekStack(varchBytes))
		if int32(value) < 0 {
			*pc = Register(addr)
		}
	case Jge:
		addr := uint32FromBytes(vm.popStack())
		value := uint32FromBytes(vm.peekStack(varchBytes))
		if int32(value) >= 0 {
			*pc = Register(addr)
		}
	case Jg:
		addr := uint32FromBytes(vm.popStack())
		value := uint32FromBytes(vm.peekStack(varchBytes))
		if int32(value) > 0 {
			*pc = Register(addr)
		}
	case Cmpu:
		compare(vm, uint32FromBytes)
	case Cmps:
		compare(vm, int32FromBytes)
	case Cmpf:
		compare(vm, float32FromBytes)
	case Writec:
		character := rune(uint32FromBytes(vm.popStack()))
		vm.stdout.WriteString(string(character))
		vm.stdout.Flush()
	case Readc:
		character, _, err := vm.stdin.ReadRune()
		if err != nil {
			vm.errcode = errIO
			return
		}
		vm.pushStack(uint32(character))
	case Exit:
		// Sets the pc to be one after the last instruction
		*pc = Register(len(vm.program))
	default:
		// Shouldn't get here since we preprocess+parse all source into
		// valid instructions before executing
		vm.errcode = errUnknownInstruction
		return
	}
}

func (vm *VM) ExecProgramDebugMode() {
	defer getDefaultRecoverFuncForVM(vm)()

	fmt.Printf("Commands:\n\tn or next: execute next instruction\n\tr or run: run program\n\tb or break <line>: break on line (or remove break on line)\n\n")

	vm.printCurrentState()

	reader := bufio.NewReader(os.Stdin)
	waitForInput := true
	breakAtLines := make(map[int]struct{})
	lastBreakLine := -1
	for {
		line := ""
		if waitForInput {
			fmt.Print("\n->")
			line, _ = reader.ReadString('\n')
			line = strings.ToLower(strings.TrimSpace(line))
		} else {
			// Check if we've reached a breakpoint
			currInstruction := int(*vm.programCounter())
			if _, ok := breakAtLines[currInstruction]; lastBreakLine != currInstruction && ok {
				fmt.Println("breakpoint")
				vm.printCurrentState()

				waitForInput = true
				lastBreakLine = currInstruction
				continue
			}
		}

		if !waitForInput || line == "n" || line == "next" {
			// Reset break flag
			lastBreakLine = -1

			vm.execNextInstruction()
			if waitForInput {
				// Only print state after each instruction if we're also waiting for input
				// after each instruction
				vm.printCurrentState()
			}

			if vm.errcode != nil {
				vm.printDebugOutput()
				fmt.Println(vm.errcode)
				return
			}
		} else if line == "program" {
			vm.printProgram()
		} else if line == "r" || line == "run" {
			waitForInput = false
		} else if strings.HasPrefix(line, "b") {
			arg := strings.Join(strings.Split(line, " ")[1:], " ")
			line, err := strconv.ParseInt(arg, 10, 32)
			if err != nil {
				fmt.Println("Unknown line number:", err)
			} else {
				_, ok := breakAtLines[int(line)]
				// If the line number has already been added, remove it
				if ok {
					delete(breakAtLines, int(line))
				} else {
					// Otherwise add it now
					breakAtLines[int(line)] = struct{}{}
				}
			}
		}
	}
}

func (vm *VM) ExecProgram() {
	defer getDefaultRecoverFuncForVM(vm)()

	for {
		vm.execNextInstruction()
		if err := vm.errcode; err != nil {
			if err != errProgramFinished {
				fmt.Println(err)
			}
			break
		}
	}
}
