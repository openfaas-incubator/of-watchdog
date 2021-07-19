// Copyright (c) OpenFaaS Author(s) 2021. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package executor

import (
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os/exec"
	"sync"
	"time"
)

// SerializingForkFunctionRunner forks a process for each invocation
type SerializingForkFunctionRunner struct {
	ExecTimeout time.Duration
	LogPrefix   bool
}

// Run run a fork for each invocation
func (f *SerializingForkFunctionRunner) Run(req FunctionRequest, w http.ResponseWriter) error {
	start := time.Now()
	functionBytes, err := serializeFunction(req, f)
	if err != nil {
		w.Header().Set("X-Duration-Seconds", fmt.Sprintf("%f", time.Since(start).Seconds()))
		w.WriteHeader(500)
		w.Write([]byte(err.Error()))
		return err
	}
	w.Header().Set("X-Duration-Seconds", fmt.Sprintf("%f", time.Since(start).Seconds()))
	w.WriteHeader(200)

	if functionBytes != nil {
		_, err = w.Write(*functionBytes)
	} else {
		log.Println("Empty function response.")
	}

	return err
}

func serializeFunction(req FunctionRequest, f *SerializingForkFunctionRunner) (*[]byte, error) {
	log.Printf("Running %s", req.Process)
	defer req.InputReader.Close()

	start := time.Now()
	cmd := exec.Command(req.Process, req.ProcessArgs...)
	cmd.Env = req.Environment

	var timer *time.Timer
	if f.ExecTimeout > time.Millisecond*0 {

		timer = time.NewTimer(f.ExecTimeout)
		go func() {
			<-timer.C

			log.Printf("Function was killed by ExecTimeout: %s\n", f.ExecTimeout.String())
			killErr := cmd.Process.Kill()
			if killErr != nil {
				log.Println("Error killing function due to ExecTimeout", killErr)
			}
		}()
	}

	if timer != nil {
		defer timer.Stop()
	}

	stdout, _ := cmd.StdoutPipe()
	stdin, _ := cmd.StdinPipe()
	stderr, _ := cmd.StderrPipe()

	err := cmd.Start()
	if err != nil {
		log.Printf("Could not start process %s", err)
		return nil, err
	}

	for {
		n, err := io.CopyN(stdin, req.InputReader, 512)
		if err != nil {
			if err == io.EOF {
				break
			}
			log.Printf("Could not copy input stream %s", err)
			break
		}
		if n == 0 {
			time.Sleep(100 * time.Nanosecond)
		}
	}
	stdin.Close()

	functionRes, functionError, errors := readOutputStreamFromProcess(stdout, stderr)

	if len(errors) > 0 {
		return nil, errors[0]
	}

	waitErr := cmd.Wait()
	if waitErr != nil {
		if functionError != nil {
			log.Printf("Stderr %s", string(*functionError))
		}
		return nil, err
	}

	if cmd.ProcessState.ExitCode() != 0 {
		log.Printf("Function exit with code %d", cmd.ProcessState.ExitCode())
		if functionError != nil {
			log.Printf("stderr %s", string(*functionError))
		}
	}

	done := time.Since(start)
	log.Printf("Took %f secs", done.Seconds())

	return functionRes, nil
}

func readOutputStreamFromProcess(stdout io.Reader, stderr io.Reader) (*[]byte, *[]byte, []error) {
	var functionResult *[]byte
	var functionError *[]byte
	var errors []error

	errChannel := make(chan error)

	go func() {
		for goErr := range errChannel {
			errors = append(errors, goErr)
		}
		close(errChannel)
	}()

	wg := sync.WaitGroup{}
	wg.Add(2)

	go func(c chan error) {
		var err error
		result, err := ioutil.ReadAll(stdout)
		functionResult = &result
		if err != nil {
			c <- err
		}

		wg.Done()
	}(errChannel)

	go func(c chan error) {
		var err error
		result, err := ioutil.ReadAll(stderr)
		functionError = &result
		if err != nil {
			c <- err
		}

		wg.Done()
	}(errChannel)

	wg.Wait()

	return functionResult, functionError, errors
}
