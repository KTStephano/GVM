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

type Instruction struct {
	code Bytecode
	// Can be used in the case where bytecode should accept 2 args - one should fit
	// into a byte and the other can use a full 32 bits
	byteArg byte
	flags   uint16
	arg     uint32
}

type Program struct {
	instructions []Instruction
	debugSymMap  map[int]string
}

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

// Flags should not use more than the first 24 bits
func NewInstruction(code Bytecode, byteArg byte, arg uint32, flags uint16) Instruction {
	return Instruction{
		code:    code,
		flags:   flags,
		byteArg: byteArg,
		arg:     arg,
	}
}

func (instr Instruction) String() string {
	code := Bytecode(instr.code)
	numArgs := instr.flags & 0xff
	if numArgs > 0 {
		intArg := int32(instr.arg)
		intArgStr := ""
		if intArg < 0 {
			// Add both the negative and unsigned version to the output
			intArgStr = fmt.Sprintf("%d (%d)", intArg, instr.arg)
		} else {
			intArgStr = fmt.Sprintf("%d", instr.arg)
		}

		if numArgs == 1 {
			return fmt.Sprintf("%s %s", code, intArgStr)
		} else {
			// When this is the case the byte argument always goes first
			return fmt.Sprintf("%s %d %s", code, instr.byteArg, intArgStr)
		}
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
				last := strings.LastIndex(args, "'")
				if last <= 0 {
					last = strings.LastIndex(args, "\"")
				}

				// Make sure the double or single quote also includes a terminating quote
				if last <= 0 {
					return nil, errors.New("unterminated character or string")
				}

				args = insertEscapeSeqReplacements(args[1:last])
				// last+1 so that we can include the end double or single quote in the first result,
				// but not in the second result
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

	if numArgs == 0 {
		return NewInstruction(code, 0, 0, 0), nil
	} else if numArgs == 1 {
		return NewInstruction(code, 0, args[0], uint16(numArgs)), nil
	} else {
		// Make sure the first argument doesn't exceed the byte arg max
		if args[0] > math.MaxUint8 {
			return Instruction{}, fmt.Errorf("%s %d is too large to fit into a byte", code, args[0])
		}

		return NewInstruction(code, byte(args[0]), args[1], uint16(numArgs)), nil
	}
}

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

	// Check for invalid register stores
	for i, instr := range instructions {
		code := Bytecode(instr.code)
		if code == Store || code == Kstore {
			if instr.arg < 2 {
				return Program{}, fmt.Errorf("illegal register write at %d: %s %d", i, code, instr.arg)
			}
		}
	}

	return Program{instructions: instructions, debugSymMap: debugSymMap}, nil
}
