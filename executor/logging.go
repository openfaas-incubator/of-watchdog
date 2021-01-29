package executor

import (
	"bufio"
	"io"
	"log"
)

const (
	// Size of the underlying buffer in bytes.
	pipeReaderBufSize = bufio.MaxScanTokenSize
)

// bindLoggingPipe spawns a goroutine for passing through logging of the given output pipe.
func bindLoggingPipe(name string, pipe io.Reader, output io.Writer) {
	log.Printf("Started logging %s from function.", name)

	reader := bufio.NewReaderSize(pipe, pipeReaderBufSize)
	logger := log.New(output, log.Prefix(), log.Flags())

	go func() {
		for {
			s, err := reader.ReadString('\n')
			if err != nil {
				if err != io.EOF {
					log.Printf("Error reading %s: %s", name, err.Error())
				}
				return
			}
			if len(s) > 0 {
				logger.Printf("%s: %s", name, s)
			}
		}
	}()
}
