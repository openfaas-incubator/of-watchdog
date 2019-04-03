package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/openfaas-incubator/of-watchdog/config"
	"github.com/openfaas-incubator/of-watchdog/executor"
)

var (
	acceptingConnections int32
)

func main() {
	atomic.StoreInt32(&acceptingConnections, 0)

	watchdogConfig, configErr := config.New(os.Environ())
	if configErr != nil {
		fmt.Fprintf(os.Stderr, configErr.Error())
		os.Exit(-1)
	}

	if len(watchdogConfig.FunctionProcess) == 0 {
		fmt.Fprintf(os.Stderr, "Provide a \"function_process\" or \"fprocess\" environmental variable for your function.\n")
		os.Exit(-1)
	}

	requestHandler := buildRequestHandler(watchdogConfig)

	log.Printf("OperationalMode: %s\n", config.WatchdogMode(watchdogConfig.OperationalMode))

	http.Handle("/", requestHandler)
	http.HandleFunc("/_/health", makeHealthHandler())

	shutdownTimeout := watchdogConfig.HTTPWriteTimeout

	s := &http.Server{
		Addr:           fmt.Sprintf(":%d", watchdogConfig.TCPPort),
		ReadTimeout:    watchdogConfig.HTTPReadTimeout,
		WriteTimeout:   watchdogConfig.HTTPWriteTimeout,
		MaxHeaderBytes: 1 << 20, // Max header of 1MB
	}

	listenUntilShutdown(shutdownTimeout, s, watchdogConfig.SuppressLock)

}

func markUnhealthy() error {
	atomic.StoreInt32(&acceptingConnections, 0)

	path := filepath.Join(os.TempDir(), ".lock")
	log.Printf("Removing lock-file : %s\n", path)
	removeErr := os.Remove(path)
	return removeErr
}

func listenUntilShutdown(shutdownTimeout time.Duration, s *http.Server, suppressLock bool) {

	idleConnsClosed := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM)

		<-sig

		log.Printf("SIGTERM received.. shutting down server in %s\n", shutdownTimeout.String())

		healthErr := markUnhealthy()

		if healthErr != nil {
			log.Printf("Unable to mark unhealthy during shutdown: %s\n", healthErr.Error())
		}

		<-time.Tick(shutdownTimeout)

		if err := s.Shutdown(context.Background()); err != nil {
			// Error from closing listeners, or context timeout:
			log.Printf("Error in Shutdown: %v", err)
		}

		log.Printf("No new connections allowed. Exiting in: %s\n", shutdownTimeout.String())

		<-time.Tick(shutdownTimeout)

		close(idleConnsClosed)
	}()

	// Run the HTTP server in a separate go-routine.
	go func() {
		if err := s.ListenAndServe(); err != http.ErrServerClosed {
			log.Printf("Error ListenAndServe: %v", err)
			close(idleConnsClosed)
		}
	}()

	if suppressLock == false {
		path, writeErr := createLockFile()

		if writeErr != nil {
			log.Panicf("Cannot write %s. To disable lock-file set env suppress_lock=true.\n Error: %s.\n", path, writeErr.Error())
		}
	} else {
		log.Println("Warning: \"suppress_lock\" is enabled. No automated health-checks will be in place for your function.")

		atomic.StoreInt32(&acceptingConnections, 1)
	}

	<-idleConnsClosed
}

func buildRequestHandler(watchdogConfig config.WatchdogConfig) http.Handler {
	var requestHandler http.HandlerFunc

	switch watchdogConfig.OperationalMode {
	case config.ModeStreaming:
		requestHandler = makeForkRequestHandler(watchdogConfig)
		break
	case config.ModeSerializing:
		requestHandler = makeSerializingForkRequestHandler(watchdogConfig)
		break
	case config.ModeAfterBurn:
		requestHandler = makeAfterBurnRequestHandler(watchdogConfig)
		break
	case config.ModeHTTP:
		requestHandler = makeHTTPRequestHandler(watchdogConfig)
		break
	default:
		log.Panicf("unknown watchdog mode: %d", watchdogConfig.OperationalMode)
		break
	}

	if watchdogConfig.ConcurrencyLimit > 0 {
		cl := &concurrencyLimiter{
			handler:          requestHandler,
			concurrencyLimit: uint64(watchdogConfig.ConcurrencyLimit),
		}
		return cl
	}

	return http.HandlerFunc(requestHandler)
}

// createLockFile returns a path to a lock file and/or an error
// if the file could not be created.
func createLockFile() (string, error) {
	path := filepath.Join(os.TempDir(), ".lock")
	log.Printf("Writing lock-file to: %s\n", path)
	writeErr := ioutil.WriteFile(path, []byte{}, 0660)

	atomic.StoreInt32(&acceptingConnections, 1)

	return path, writeErr
}

func makeAfterBurnRequestHandler(watchdogConfig config.WatchdogConfig) func(http.ResponseWriter, *http.Request) {

	commandName, arguments := watchdogConfig.Process()
	functionInvoker := executor.AfterBurnFunctionRunner{
		Process:     commandName,
		ProcessArgs: arguments,
	}

	log.Printf("Forking %s %s\n", commandName, arguments)
	functionInvoker.Start()

	return func(w http.ResponseWriter, r *http.Request) {

		req := executor.FunctionRequest{
			Process:      commandName,
			ProcessArgs:  arguments,
			InputReader:  r.Body,
			OutputWriter: w,
		}

		functionInvoker.Mutex.Lock()

		err := functionInvoker.Run(req, r.ContentLength, r, w)

		if err != nil {
			w.WriteHeader(500)
			w.Write([]byte(err.Error()))
		}

		functionInvoker.Mutex.Unlock()
	}
}

func makeSerializingForkRequestHandler(watchdogConfig config.WatchdogConfig) func(http.ResponseWriter, *http.Request) {
	functionInvoker := executor.SerializingForkFunctionRunner{
		ExecTimeout: watchdogConfig.ExecTimeout,
	}

	return func(w http.ResponseWriter, r *http.Request) {

		var environment []string

		if watchdogConfig.InjectCGIHeaders {
			environment = getEnvironment(r)
		}

		commandName, arguments := watchdogConfig.Process()
		req := executor.FunctionRequest{
			Process:       commandName,
			ProcessArgs:   arguments,
			InputReader:   r.Body,
			ContentLength: &r.ContentLength,
			OutputWriter:  w,
			Environment:   environment,
		}

		w.Header().Set("Content-Type", watchdogConfig.ContentType)
		err := functionInvoker.Run(req, w)
		if err != nil {
			log.Println(err)
		}
	}
}

func makeForkRequestHandler(watchdogConfig config.WatchdogConfig) func(http.ResponseWriter, *http.Request) {
	functionInvoker := executor.ForkFunctionRunner{
		ExecTimeout: watchdogConfig.ExecTimeout,
	}

	return func(w http.ResponseWriter, r *http.Request) {

		var environment []string

		if watchdogConfig.InjectCGIHeaders {
			environment = getEnvironment(r)
		}

		commandName, arguments := watchdogConfig.Process()
		req := executor.FunctionRequest{
			Process:      commandName,
			ProcessArgs:  arguments,
			InputReader:  r.Body,
			OutputWriter: w,
			Environment:  environment,
		}

		w.Header().Set("Content-Type", watchdogConfig.ContentType)
		err := functionInvoker.Run(req)
		if err != nil {
			log.Println(err.Error())

			// Probably cannot write to client if we already have written a header
			// w.WriteHeader(500)
			// w.Write([]byte(err.Error()))
		}
	}
}

func getEnvironment(r *http.Request) []string {
	var envs []string

	envs = os.Environ()
	for k, v := range r.Header {
		kv := fmt.Sprintf("Http_%s=%s", strings.Replace(k, "-", "_", -1), v[0])
		envs = append(envs, kv)
	}
	envs = append(envs, fmt.Sprintf("Http_Method=%s", r.Method))

	if len(r.URL.RawQuery) > 0 {
		envs = append(envs, fmt.Sprintf("Http_Query=%s", r.URL.RawQuery))
	}

	if len(r.URL.Path) > 0 {
		envs = append(envs, fmt.Sprintf("Http_Path=%s", r.URL.Path))
	}

	return envs
}

func makeHTTPRequestHandler(watchdogConfig config.WatchdogConfig) func(http.ResponseWriter, *http.Request) {
	commandName, arguments := watchdogConfig.Process()
	functionInvoker := executor.HTTPFunctionRunner{
		ExecTimeout:    watchdogConfig.ExecTimeout,
		Process:        commandName,
		ProcessArgs:    arguments,
		BufferHTTPBody: watchdogConfig.BufferHTTPBody,
	}

	if len(watchdogConfig.UpstreamURL) == 0 {
		log.Fatal(`For mode=http you must specify a valid URL for "upstream_url"`)
	}

	urlValue, upstreamURLErr := url.Parse(watchdogConfig.UpstreamURL)
	if upstreamURLErr != nil {
		log.Fatal(upstreamURLErr)
	}
	functionInvoker.UpstreamURL = urlValue

	fmt.Printf("Forking - %s %s\n", commandName, arguments)
	functionInvoker.Start()

	return func(w http.ResponseWriter, r *http.Request) {

		req := executor.FunctionRequest{
			Process:      commandName,
			ProcessArgs:  arguments,
			InputReader:  r.Body,
			OutputWriter: w,
		}

		if r.Body != nil {
			defer r.Body.Close()
		}

		err := functionInvoker.Run(req, r.ContentLength, r, w)

		if err != nil {
			w.WriteHeader(500)
			w.Write([]byte(err.Error()))
		}

	}
}

func lockFilePresent() bool {
	path := filepath.Join(os.TempDir(), ".lock")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false
	}
	return true
}

func makeHealthHandler() func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if atomic.LoadInt32(&acceptingConnections) == 0 || lockFilePresent() == false {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}

			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK"))

			break
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

type concurrencyLimiter struct {
	handler http.HandlerFunc
	/*
		We keep two counters here in order to make it so that we can know when a request has gone to completed
		in the tests. We could wrap these up in a condvar, so there's no need to spinlock, but that seems overkill
		for testing.

		Otherwise, there's all sorts of futzing in order to make sure that the concurrency limiter handler
		has completed
		The math works on overflow:
			var x, y uint64
			x = (1 << 64 - 1)
			y = (1 << 64 - 1)
			x++
			fmt.Println(x)
			fmt.Println(y)
			fmt.Println(x - y)
		Prints:
			0
			18446744073709551615
			1
	*/
	requestsStarted   uint64
	requestsCompleted uint64

	concurrencyLimit uint64
}

func (cl *concurrencyLimiter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestsStarted := atomic.AddUint64(&cl.requestsStarted, 1)
	completedRequested := atomic.LoadUint64(&cl.requestsCompleted)
	if requestsStarted-completedRequested > cl.concurrencyLimit {
		// This is a failure pathway, and we do not want to block on the write to finish
		atomic.AddUint64(&cl.requestsCompleted, 1)
		w.WriteHeader(429)
		fmt.Fprintf(w, "Concurrent request limit exceeded. Max concurrent requests: %d\n", cl.concurrencyLimit)
		return
	}
	cl.handler(w, r)
	atomic.AddUint64(&cl.requestsCompleted, 1)
}
