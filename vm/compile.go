package gvm

import (
	"bufio"
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Laid out so that sizeof(Instruction) == 8 is possible
type Instruction struct {
	// Code embeds upper 8 bits as number of op args, lower 8 bits as bytecode instruction
	code uint16
	// Register is used for things like load, store, raddi, etc.
	register uint16
	// Normal 32-bit argument for inlining constants
	arg uint32
}

type Program struct {
	instructions []Instruction
	debugSymMap  map[int]string
}

const (
	nopNoArgs uint16 = uint16(Nop)

	byteOneArg   uint16 = 0x0100 | uint16(Byte)
	constOneArg  uint16 = 0x0100 | uint16(Const)
	loadOneArg   uint16 = 0x0100 | uint16(Load)
	storeOneArg  uint16 = 0x0100 | uint16(Store)
	kstoreOneArg uint16 = 0x0100 | uint16(Kstore)

	loadp8NoArgs   uint16 = uint16(Loadp8)
	loadp16NoArgs  uint16 = uint16(Loadp16)
	loadp32NoArgs  uint16 = uint16(Loadp32)
	storep8NoArgs  uint16 = uint16(Storep8)
	storep16NoArgs uint16 = uint16(Storep16)
	storep32NoArgs uint16 = uint16(Storep32)

	pushNoArgs uint16 = uint16(Push)
	pushOneArg uint16 = 0x0100 | uint16(Push)
	popNoArgs  uint16 = uint16(Pop)
	popOneArg  uint16 = 0x0100 | uint16(Pop)

	addiNoArgs uint16 = uint16(Addi)
	addiOneArg uint16 = 0x0100 | uint16(Addi)
	addfNoArgs uint16 = uint16(Addf)
	addfOneArg uint16 = 0x0100 | uint16(Addf)

	subiNoArgs uint16 = uint16(Subi)
	subiOneArg uint16 = 0x0100 | uint16(Subi)
	subfNoArgs uint16 = uint16(Subf)
	subfOneArg uint16 = 0x0100 | uint16(Subf)

	muliNoArgs uint16 = uint16(Muli)
	muliOneArg uint16 = 0x0100 | uint16(Muli)
	mulfNoArgs uint16 = uint16(Mulf)
	mulfOneArg uint16 = 0x0100 | uint16(Mulf)

	diviNoArgs uint16 = uint16(Divi)
	diviOneArg uint16 = 0x0100 | uint16(Divi)
	divfNoArgs uint16 = uint16(Divf)
	divfOneArg uint16 = 0x0100 | uint16(Divf)

	remuNoArgs uint16 = uint16(Remu)
	remuOneArg uint16 = 0x0100 | uint16(Remu)
	remsNoArgs uint16 = uint16(Rems)
	remsOneArg uint16 = 0x0100 | uint16(Rems)
	remfNoArgs uint16 = uint16(Remf)
	remfOneArg uint16 = 0x0100 | uint16(Remf)

	notNoArgs uint16 = uint16(Not)
	andNoArgs uint16 = uint16(And)
	andOneArg uint16 = 0x0100 | uint16(And)
	orNoArgs  uint16 = uint16(Or)
	orOneArg  uint16 = 0x0100 | uint16(Or)
	xorNoArgs uint16 = uint16(Xor)
	xorOneArg uint16 = 0x0100 | uint16(Xor)

	shiftLNoArgs uint16 = uint16(Shiftl)
	shiftLOneArg uint16 = 0x0100 | uint16(Shiftl)
	shiftRNoArgs uint16 = uint16(Shiftr)
	shiftROneArg uint16 = 0x0100 | uint16(Shiftr)

	jmpNoArgs uint16 = uint16(Jmp)
	jmpOneArg uint16 = 0x0100 | uint16(Jmp)
	jzNoArgs  uint16 = uint16(Jz)
	jzOneArg  uint16 = 0x0100 | uint16(Jz)
	jnzNoArgs uint16 = uint16(Jnz)
	jnzOneArg uint16 = 0x0100 | uint16(Jnz)
	jleNoArgs uint16 = uint16(Jle)
	jleOneArg uint16 = 0x0100 | uint16(Jle)
	jlNoArgs  uint16 = uint16(Jl)
	jlOneArg  uint16 = 0x0100 | uint16(Jl)
	jgeNoArgs uint16 = uint16(Jge)
	jgeOneArg uint16 = 0x0100 | uint16(Jge)
	jgNoArgs  uint16 = uint16(Jg)
	jgOneArg  uint16 = 0x0100 | uint16(Jg)

	cmpuNoArgs uint16 = uint16(Cmpu)
	cmpsNoArgs uint16 = uint16(Cmps)
	cmpfNoArgs uint16 = uint16(Cmpf)

	writebNoArgs uint16 = uint16(Writeb)
	writecNoArgs uint16 = uint16(Writec)
	flushNoArgs  uint16 = uint16(Flush)
	readcNoArgs  uint16 = uint16(Readc)

	raddiOneArg  uint16 = 0x0100 | uint16(Raddi)
	raddiTwoArgs uint16 = 0x0200 | uint16(Raddi)
	raddfOneArg  uint16 = 0x0100 | uint16(Raddf)
	raddfTwoArgs uint16 = 0x0200 | uint16(Raddf)

	rsubiOneArg  uint16 = 0x0100 | uint16(Rsubi)
	rsubiTwoArgs uint16 = 0x0200 | uint16(Rsubi)
	rsubfOneArg  uint16 = 0x0100 | uint16(Rsubf)
	rsubfTwoArgs uint16 = 0x0200 | uint16(Rsubf)

	rmuliOneArg  uint16 = 0x0100 | uint16(Rmuli)
	rmuliTwoArgs uint16 = 0x0200 | uint16(Rmuli)
	rmulfOneArg  uint16 = 0x0100 | uint16(Rmulf)
	rmulfTwoArgs uint16 = 0x0200 | uint16(Rmulf)

	rdiviOneArg  uint16 = 0x0100 | uint16(Rdivi)
	rdiviTwoArgs uint16 = 0x0200 | uint16(Rdivi)
	rdivfOneArg  uint16 = 0x0100 | uint16(Rdivf)
	rdivfTwoArgs uint16 = 0x0200 | uint16(Rdivf)

	rshiftLOneArg  uint16 = 0x0100 | uint16(Rshiftl)
	rshiftLTwoArgs uint16 = 0x0200 | uint16(Rshiftl)
	rshiftROneArg  uint16 = 0x0100 | uint16(Rshiftr)
	rshiftRTwoargs uint16 = 0x0200 | uint16(Rshiftr)

	exitNoArgs uint16 = uint16(Exit)
)

// Allows us to easily find and replace commands from start to end of line
var (
	comments = regexp.MustCompile("//.*")

	// Allows us to replace \\* escape sequence with \*, such as \\n -> \n
	// (happens when reading from console or file)
	escapeSeqReplacements = map[string]string{
		"\\a":  "\a",
		"\\b":  "\b",
		"\\t":  "\t",
		"\\n":  "\n",
		"\\r":  "\r",
		"\\f":  "\f",
		"\\v":  "\v",
		"\\\"": "\"",
	}
)

// Instructions are made up of:
//
//		first 16 bits => numArgs, bytecode
//		next 16 bits => register index if applicable
//		next 32 bits => oparg
//
//	 total => 64 bits per instruction (fixed size)
func NewInstruction(numArgs byte, code Bytecode, register uint16, arg uint32) Instruction {
	return Instruction{
		code:     (uint16(numArgs) << 8) | uint16(code),
		register: register,
		arg:      arg,
	}
}

func (instr Instruction) String() string {
	// Lower 8 bits are the bytecode, upper 8 bits are the number of op args
	code := Bytecode(instr.code & 0xff)
	numArgs := (instr.code & 0xff00) >> 8
	if numArgs > 0 {
		intArg := int32(instr.arg)
		intArgStr := ""
		// Move the set int arg into a function since we call of from 2 separate
		// code branches
		setIntArgStr := func() {
			if intArg < 0 {
				// Add both the negative and unsigned version to the output
				intArgStr = fmt.Sprintf(" %d (%d)", intArg, instr.arg)
			} else {
				intArgStr = fmt.Sprintf(" %d", instr.arg)
			}
		}

		registerStr := ""
		if code.IsRegisterOp() {
			registerStr = fmt.Sprintf(" %d", instr.register)

			// Some instructions accept both a register and an additional argument
			if numArgs > 1 {
				setIntArgStr()
			}
		} else if numArgs > 1 {
			setIntArgStr()
		}

		return fmt.Sprintf("%s%s%s", code, registerStr, intArgStr)
	} else {
		// No op arg - only include code string
		return code.String()
	}
}

// Checks for things like \\n and replaces it with \n
func insertEscapeSeqReplacements(line string) string {
	for orig, replace := range escapeSeqReplacements {
		line = strings.ReplaceAll(line, orig, replace)
	}
	return line
}

// Checks for things like \n and replaces it with \\n
func revertEscapeSeqReplacements(line string) string {
	for orig, replace := range escapeSeqReplacements {
		line = strings.ReplaceAll(line, replace, orig)
	}
	return line
}

// Responsible for removing comments and whitespace and splitting an instruction into (instruction, argument0, argument1) triples
func preprocessLine(line string, labels map[*regexp.Regexp]string, lines [][3]string, debugSym map[int]string) ([][3]string, error) {
	line = comments.ReplaceAllString(line, "")
	line = strings.TrimSpace(line)

	// Check if the line was pure whitespace
	if line == "" {
		return lines, nil
		// Check if the line is a label
	} else if strings.HasSuffix(line, ":") {
		// Get rid of the : in the label
		label := strings.ReplaceAll(line, ":", "")

		// Make sure the label doesn't contain any inner whitespace
		if strings.ContainsFunc(label, unicode.IsSpace) {
			return nil, fmt.Errorf("invalid label (inner whitespace not allowed): %s", line)
		}

		// Compile into a regex pattern that only matches on label<word boundary> where label is at the beginning
		// of the argument (so label inside of a string or quotes will be ignored)
		r, err := regexp.Compile(fmt.Sprintf(`^%s\b`, label))
		if err != nil {
			return nil, fmt.Errorf("invalid label: %s", line)
		}

		labels[r] = fmt.Sprintf("%d", len(lines))
		if debugSym != nil {
			debugSym[len(lines)] = label
			// For debug symbols we add a nop so that we can preserve this line in the code
			return append(lines, [3]string{"nop", "", ""}), nil
		} else {
			return lines, nil
		}
	} else {
		split := strings.Split(line, " ")
		code := split[0]
		resultArgs := [2]string{}
		if len(split) > 1 {
			// Rejoin rest of split array into 1 string
			args := strings.Join(split[1:], " ")

			// If it starts with a double or single quote, insert escape sequence replacements
			if strings.HasPrefix(args, "'") || strings.HasPrefix(args, "\"") {
				guardC := args[0]
				last := strings.LastIndex(args, string(guardC))

				// Make sure the double or single quote also includes a terminating quote
				if last <= 0 {
					return nil, errors.New("unterminated character or string")
				}

				// Insert escape sequence replacements for the characters in between the quotes
				// re-add the quotes after (that's what guardC holds)
				args = fmt.Sprintf("%c%s%c", guardC, insertEscapeSeqReplacements(args[1:last]), guardC)

				// Recompute the last index since escape sequence replacements may have changed the string length
				last = strings.LastIndex(args, string(guardC))

				// last+1 so that the end single/double quote can be included
				resultArgs[0] = args[:last+1]
				resultArgs[1] = strings.TrimSpace(args[last+1:])
			} else {
				// Since we know the first arg wasn't quoted, remaining inputs should be numbers or labels
				// which fit perfectly into 1 or 2 arguments
				if len(split[1:]) > 2 {
					return nil, errors.New("too many or invalid type of arguments to instruction")
				}

				for i := 0; i < len(split[1:]); i++ {
					resultArgs[i] = strings.TrimSpace(split[1:][i])
				}
			}
		}

		// If the instruction is `const arg` and the argument is a string,
		// expand the instruction to be a series of `byte arg` instructions
		//
		// We need to do the expansion in the preprocess stage or the labels
		// will end up pointing to the wrong instructions
		if code == Const.String() && strings.HasPrefix(resultArgs[0], "\"") && strings.HasSuffix(resultArgs[0], "\"") {
			bytes := []byte(resultArgs[0])
			// Slice bytes to get rid of start and end quotes
			bytes = bytes[1 : len(bytes)-1]

			// Append instructions in reverse order so that the top value on the
			// stack corresponds to the start of the string
			for i := len(bytes) - 1; i >= 0; i-- {
				if debugSym != nil {
					// Since it's a debug symbol, add back the escaped characters
					debugSym[len(lines)] = revertEscapeSeqReplacements(fmt.Sprintf("%s '%c'", Byte.String(), bytes[i]))
				}

				lines = append(lines, [3]string{Byte.String(), fmt.Sprintf("%d", bytes[i]), resultArgs[1]})
			}
		} else {
			if debugSym != nil {
				debugSym[len(lines)] = line
			}

			// Forward result args unchanged
			lines = append(lines, [3]string{code, resultArgs[0], resultArgs[1]})
		}

		return lines, nil
	}
}

// Converts 1 piece of input into a uint32. In the case of floats, it will be the unsigned bit representation.
//
// This function should be called only after all labels have been removed from the source arguments.
func inputArgToUint32(strArg string) (uint32, error) {
	if strArg == "" {
		return math.MaxUint32, errors.New("invalid")
	}

	// Character - replace with number
	if strings.HasPrefix(strArg, "'") {
		runes := []rune(strArg)
		// first rune should be quote, then number, then end quote (len == 3)
		if len(runes) != 3 {
			return math.MaxUint32, errors.New("character is too large to fit into 32 bits")
		}

		return uint32(runes[1]), nil
	} else {
		// Likely a regular number or float
		if strings.Contains(strArg, ".") {
			arg, err := strconv.ParseFloat(strArg, 32)
			if err != nil {
				return math.MaxUint32, err
			}

			return math.Float32bits(float32(arg)), nil
		} else {
			var arg int64
			var err error
			base := 10
			// Check for hex values
			if strings.HasPrefix(strArg, "0x") {
				base = 16
				// Remove 0x from input
				strArg = strings.Replace(strArg, "0x", "", 1)
			}

			arg, err = strconv.ParseInt(strArg, base, 32)
			if err != nil {
				return math.MaxUint32, err
			}

			return uint32(arg), nil
		}
	}
}

// Converts an input line from list of strings to a VM instruction
//
// This function should be called only after all labels have been removed from the source arguments
func parseInputLine(line [3]string) (Instruction, error) {
	code, ok := strToInstrMap[line[0]]
	if !ok {
		return Instruction{}, fmt.Errorf("unknown bytecode: %s", line[0])
	}

	// Run through each argument and try to convert them to uint32
	args := [2]uint32{}
	numArgs := 0
	for i, arg := range line[1:] {
		if arg != "" {
			numArgs++
			n, err := inputArgToUint32(arg)
			if err != nil {
				return Instruction{}, err
			}

			args[i] = n
		}
	}

	// Make sure the number of arguments we received makes sense for this instruction
	if maxArgs := code.NumRequiredOpArgs() + code.NumOptionalOpArgs(); numArgs < code.NumRequiredOpArgs() {
		return Instruction{}, fmt.Errorf("%s wanted %d args but only got %d", code, code.NumRequiredOpArgs(), numArgs)
	} else if numArgs > maxArgs {
		return Instruction{}, fmt.Errorf("%s can only support a max of %d args but got %d", code, maxArgs, numArgs)
	}

	if code.IsRegisterOp() {
		// Register instructions accept a minimum of 1 16-bit argument, but some also have an optional
		// 2nd 32-bit argument
		return NewInstruction(byte(numArgs), code, uint16(args[0]), args[1]), nil
	} else {
		// Non-register instructions only accept 1 32-bit argument
		return NewInstruction(byte(numArgs), code, 0, args[0]), nil
	}
}

// Takes a series of files and combines them into a program represented by a list of instructions
// and a debug symbol map (if debug requested). The files are read sequentially so the first instruction
// in the first file is what starts executing first.
func CompileSource(debug bool, files ...string) (Program, error) {
	// If requested, set up the VM in debug mode
	var debugSymMap map[int]string
	if debug {
		debugSymMap = make(map[int]string)
	}

	// Read each file
	lines := make([]string, 0)
	for _, filename := range files {
		file, err := os.Open(filename)
		if err != nil {
			fmt.Println("Could not read", filename)
			return Program{}, err
		}

		reader := bufio.NewReader(file)
		for {
			line, _, err := reader.ReadLine()
			if err != nil {
				break
			}

			lines = append(lines, string(line))
		}
	}

	// Maps from regex(label) -> address string
	labels := make(map[*regexp.Regexp]string)
	preprocessedLines := make([][3]string, 0)

	// First preprocess line to remove whitespace lines and convert labels
	// into line numbers
	for _, line := range lines {
		var err error
		preprocessedLines, err = preprocessLine(string(line), labels, preprocessedLines, debugSymMap)
		if err != nil {
			return Program{}, err
		}
	}

	instructions := make([]Instruction, 0, len(preprocessedLines))

	// Parse each input line to generate a list of instructions
	for _, line := range preprocessedLines {
		// Replace all labels with their instruction address
		for label, lineNum := range labels {
			for i := range line[1:] {
				// i+1 since line[1:] reduces the length by 1, but the main array
				// is unchanged in size so we're off by 1
				line[i+1] = label.ReplaceAllString(line[i+1], lineNum)
			}
		}

		instr, err := parseInputLine(line)
		if err != nil {
			return Program{}, err
		}

		instructions = append(instructions, instr)
	}

	// Check for invalid register stores (need to keep program counter and stack pointer
	// from being written over by the input code)
	for i, instr := range instructions {
		code := Bytecode(instr.code & 0xff)
		errVal := fmt.Errorf("illegal/invalid register write at %d: %s", i, instr)
		if code.IsRegisterOp() {
			if instr.register < 2 || instr.register > uint16(numRegisters) {
				return Program{}, errVal
			}
		}
	}

	return Program{instructions: instructions, debugSymMap: debugSymMap}, nil
}
