package main

import (
	"flag"
	"fmt"
	gvm "gvm/vm"
	"os"
)

// Allows us to go into debug mode when needed
var debugVM = flag.Bool("debug", false, "Enter into debug mode")

func main() {
	// Uncomment for CPU profiling (also shows you what was inlined vs not inlined)
	// f, err := os.Create("pprof.cpu")
	// if err != nil {
	// 	log.Fatal(err)
	// }
	// pprof.StartCPUProfile(f)
	// defer pprof.StopCPUProfile()

	flag.Parse()
	args := os.Args[len(os.Args)-flag.NArg():]

	// Use os.Args to accept list of files. This should still work when/if
	// additional arguments are added (at that point using package flag) because
	// it will tell us how many arguments are remaining after it has finished parsing.
	//
	// First argument is the path to the program
	if len(args) == 0 {
		fmt.Println("Usage: <file 1> [file 2] [file 3] ... [file N]")
		return
	}

	program, err := gvm.CompileSource(*debugVM, args...)
	if err != nil {
		fmt.Println(err)
		return
	}

	vm := gvm.NewVirtualMachine(program)
	if *debugVM {
		vm.RunProgramDebugMode()
	} else {
		vm.RunProgram()
	}
}
