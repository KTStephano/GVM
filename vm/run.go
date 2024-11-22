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

			fmt.Printf("%s%s\n", err, formatInstructionStr(vm, *vm.pc, " at instruction:"))
		}
	}
}

func (vm *VM) RunProgramDebugMode() {
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
			currInstruction := int(*vm.pc)
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

			vm.execInstructions(true)
			if waitForInput {
				// Only print state after each instruction if we're also waiting for input
				// after each instruction
				vm.printCurrentState()
			}

			if vm.errcode != nil {
				vm.printDebugOutput()
				if vm.errcode != errProgramFinished {
					// pc-1 should be the instruction that failed
					fmt.Println(formatInstructionStr(vm, *vm.pc-1, vm.errcode.Error()))
				}

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

func (vm *VM) RunProgram() {
	defer getDefaultRecoverFuncForVM(vm)()

	vm.execInstructions(false)
	if err := vm.errcode; err != nil {
		if err != errProgramFinished {
			fmt.Println(err)
		}
	}

	// for {
	// 	vm.execNextInstruction(false)
	// 	if err := vm.errcode; err != nil {
	// 		if err != errProgramFinished {
	// 			fmt.Println(err)
	// 		}
	// 		break
	// 	}
	// }
}
