package executor

import (
	"bufio"
	"io"
	"log"
	"time"
)

var stdLogBufferSize = 64 * 1024

// logHeader creates the log prefix timestamp. This is uses the
// format 2006/01/02 15:04:05
var logHeader = func() []byte {
	return []byte(time.Now().Format("2006/01/02 15:04:05"))
}

// bindLoggingPipe spawns a goroutine for passing through logging of the given output pipe.
//
// This implementation is adapted from https://github.com/hashicorp/go-plugin/pull/98
func bindLoggingPipe(name string, pipe io.Reader, output io.Writer) {
	log.Printf("Started logging %s from function.", name)

	scanner := bufio.NewReaderSize(pipe, stdLogBufferSize)

	go func() {
		continuation := false

		for {
			line, isPrefix, err := scanner.ReadLine()
			switch {
			case err == io.EOF:
				return
			case err != nil:
				log.Printf("Error scanning %s: %s", name, err.Error())
				return
			}

			if !continuation {
				// we are not continuing a previous line, so we
				// start the log line with the log prefix
				_, _ = output.Write(logHeader())
			}

			_, _ = output.Write(line)
			// the line is longer than stdLogBufferSize
			if isPrefix || continuation {

				// we are continuing a long log line,
				// but it is the end of the actual line,
				// so put the newline back in
				if !isPrefix {
					_, _ = output.Write([]byte{'\n'})
				}

				continuation = isPrefix
				continue
			}

			// not a prefix or continuation, so we have written a complete line
			// so put the newline back in
			_, _ = output.Write([]byte{'\n'})
		}
	}()
}
