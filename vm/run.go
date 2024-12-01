package gvm

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func (vm *VM) RunProgramDebugMode() {
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

			result := vm.execInstructions(true)
			if waitForInput {
				// Only print state after each instruction if we're also waiting for input
				// after each instruction
				vm.printCurrentState()
			}

			if result {
				continue
			} else if vm.errcode != nil {
				vm.printDebugOutput()
				if vm.errcode != errSystemShutdown {
					// pc-instructionBytes should be the instruction that failed
					fmt.Println(formatInstructionStr(vm, *vm.pc-instructionBytes, vm.errcode.Error()))
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
	// While execInstructions returns true, keep allowing it to execute
	// (sometimes it temporarily returns when it is trying to recover from an error)
	for vm.execInstructions(false) {
	}

	if err := vm.errcode; err != nil {
		if err != errSystemShutdown {
			// pc-instructionBytes should be the instruction that failed
			fmt.Println(formatInstructionStr(vm, *vm.pc-instructionBytes, vm.errcode.Error()))
		}
	}
}
