package executor

import (
	"bytes"
	"fmt"
	"log"
	"strings"
	"testing"
	"time"
)

func TestBindLoggingPipe(t *testing.T) {
	// set the log timestamp prefix to just the date to make
	// checking the output deterministic
	now := time.Now().Format("2006/01/02 15:04:05")
	logHeader = func() []byte {
		return []byte(now)
	}

	// make the buffer small so that the test stays readable
	orig := stdLogBufferSize
	stdLogBufferSize = 32
	defer func() {
		stdLogBufferSize = orig
		log.SetFlags(log.LstdFlags)
	}()

	// test several empty lines, a long line and a short line
	msg := `
this line is more than 32 bytes long

this line is short
`

	reader := strings.NewReader(msg)

	out := bytes.Buffer{}
	bindLoggingPipe("TestFunc", reader, &out)

	// give the pipe time to actually parse the logs
	time.Sleep(1 * time.Second)

	ouput := out.String()

	expected := fmt.Sprintf(`%s
%sthis line is more than 32 bytes long
%s
%sthis line is short
`, now, now, now, now)

	if ouput != expected {
		t.Fatalf("incorrect log output: expected \n%q, got \n%q", expected, ouput)
	}

}
