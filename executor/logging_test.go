package executor

import (
	"fmt"
	"io"
	"io/ioutil"
	"strings"
	"testing"
	"time"
)

//test bindLoggingPipe can process lines longer than pipeReaderBufSize
func TestBindLoggingPipeHandlesLongLines(t *testing.T) {
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()

	bindLoggingPipe("stderr", r, ioutil.Discard)

	done := make(chan bool)
	go func() {
		defer close(done)
		lengths := []int{0, 1, 2, pipeReaderBufSize / 2, pipeReaderBufSize - 1, pipeReaderBufSize, pipeReaderBufSize + 1, pipeReaderBufSize * 2}
		for _, l := range lengths {
			_, err := w.Write([]byte(fmt.Sprintf("%s\n", strings.Repeat("x", l))))
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatalf("Pipe writer close error: %v", err)
		}
	}()

	select {
	case <-done:
		return
	case <-time.After(3 * time.Second):
		t.Fatal("Write to pipe is hanging")
	}
}
