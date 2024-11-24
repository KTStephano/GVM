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

const (
	hasOptionalArg uint16 = 0x0001
)

// Flags should not use more than the first 24 bits
func NewInstruction(code Bytecode, arg uint32, flags uint16) Instruction {
	return Instruction{
		code:  code,
		flags: flags,
		arg:   arg,
	}
}

func (instr Instruction) String() string {
	code := Bytecode(instr.code)
	if code.RequiresOpArg() || (code.OptionalOpArg() && instr.flags > 0) {
		intArg := int32(instr.arg)
		if intArg < 0 {
			// Add both the negative and unsigned version to the output
			return fmt.Sprintf("%s %d (%d)", code.String(), intArg, instr.arg)
		}
		// Only include the unsigned version
		return fmt.Sprintf("%s %d", code.String(), instr.arg)
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

func preprocessLine(line string, labels map[*regexp.Regexp]string, lines [][2]string, debugSym map[int]string) ([][2]string, error) {
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
			return append(lines, [2]string{"nop", ""}), nil
		} else {
			return lines, nil
		}
	} else {
		split := strings.Split(line, " ")
		code := split[0]
		args := ""
		if len(split) > 1 {
			// Rejoin rest of split array into 1 string
			args = strings.Join(split[1:], " ")

			// If it starts with a double or single quote, insert escape sequence replacements
			if strings.HasPrefix(args, "'") || strings.HasPrefix(args, "\"") {
				// Make sure the double or single quote also includes a terminating quote
				if !strings.HasSuffix(args, "'") && !strings.HasSuffix(args, "\"") {
					return nil, errors.New("unterminated character or string")
				}

				args = insertEscapeSeqReplacements(args)
			}
		}

		// If the instruction is `const arg` and the argument is a string,
		// expand the instruction to be a series of `byte arg` instructions
		//
		// We need to do the expansion in the preprocess stage or the labels
		// will end up pointing to the wrong instructions
		if code == Const.String() && strings.HasPrefix(args, "\"") && strings.HasSuffix(args, "\"") {
			bytes := []byte(args)
			// Slice bytes to get rid of start and end quotes
			bytes = bytes[1 : len(bytes)-1]

			// Append instructions in reverse order so that the top value on the
			// stack corresponds to the start of the string
			for i := len(bytes) - 1; i >= 0; i-- {
				if debugSym != nil {
					// Since it's a debug symbol, add back the escaped characters
					debugSym[len(lines)] = revertEscapeSeqReplacements(fmt.Sprintf("%s '%c'", Byte.String(), bytes[i]))
				}

				lines = append(lines, [2]string{Byte.String(), fmt.Sprintf("%d", bytes[i])})
			}
		} else {
			if debugSym != nil {
				debugSym[len(lines)] = line
			}

			lines = append(lines, [2]string{code, args})
		}

		return lines, nil
	}
}

func parseInputLine(line [2]string) (Instruction, error) {
	code, ok := strToInstrMap[line[0]]
	if !ok {
		return Instruction{}, fmt.Errorf("unknown bytecode: %s", line[0])
	}

	strArg := line[1]
	if strArg != "" {
		if !code.RequiresOpArg() && !code.OptionalOpArg() {
			return Instruction{}, fmt.Errorf("%s does not allow an op argument", code.String())
		}

		// Character - replace with number
		if strings.HasPrefix(strArg, "'") {
			runes := []rune(strArg)
			// first rune should be quote, then number, then end quote (len == 3)
			if len(runes) != 3 {
				return Instruction{}, errors.New("character is too large to fit into 32 bits")
			}

			return NewInstruction(code, uint32(runes[1]), hasOptionalArg), nil
		} else {
			// Likely a regular number or float
			if strings.Contains(strArg, ".") {
				arg, err := strconv.ParseFloat(strArg, 32)
				if err != nil {
					return Instruction{}, err
				}

				return NewInstruction(code, math.Float32bits(float32(arg)), hasOptionalArg), nil
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
					return Instruction{}, err
				}

				return NewInstruction(code, uint32(arg), hasOptionalArg), nil
			}
		}
	} else {
		if code.RequiresOpArg() {
			return Instruction{}, fmt.Errorf("%s requires an op argument", code.String())
		}

		return NewInstruction(code, 0, 0), nil
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
	preprocessedLines := make([][2]string, 0)

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
			line[1] = label.ReplaceAllString(line[1], lineNum)
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
