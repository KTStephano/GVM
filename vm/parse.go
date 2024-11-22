package gvm

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

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

func preprocessLine(line string, comments *regexp.Regexp, labels map[string]string, lines [][2]string, debugSym map[int]string) ([][2]string, error) {
	line = comments.ReplaceAllString(line, "")
	line = strings.TrimSpace(line)

	// Check if the line was pure whitespace
	if line == "" {
		return lines, nil
		// Check if the line is a label
	} else if strings.HasSuffix(line, ":") {
		// Get rid of the : in the label
		label := strings.ReplaceAll(line, ":", "")
		labels[label] = fmt.Sprintf("%d", len(lines))
		if debugSym != nil {
			debugSym[len(lines)] = label
		}
		return append(lines, [2]string{"nop", ""}), nil
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
		if !code.RequiresOpArg() {
			return Instruction{}, fmt.Errorf("%s does not require an op argument", code.String())
		}

		// Character - replace with number
		if strings.HasPrefix(strArg, "'") {
			runes := []rune(strArg)
			// first rune should be quote, then number, then end quote (len == 3)
			if len(runes) != 3 {
				return Instruction{}, errors.New("character is too large to fit into 32 bits")
			}

			return NewInstruction(code, uint32(runes[1])), nil
		} else {
			// Likely a regular number or float
			if strings.Contains(strArg, ".") {
				arg, err := strconv.ParseFloat(strArg, 32)
				if err != nil {
					return Instruction{}, err
				}

				return NewInstruction(code, math.Float32bits(float32(arg))), nil
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

				return NewInstruction(code, uint32(arg)), nil
			}
		}
	} else {
		if code.RequiresOpArg() {
			return Instruction{}, fmt.Errorf("%s requires an op argument", code.String())
		}

		return NewInstruction(code, 0), nil
	}
}
