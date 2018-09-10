# of-watchdog

This is a re-write of the OpenFaaS watchdog.

[Original Watchdog source-code](https://github.com/openfaas/faas/tree/master/watchdog)

### Goals:
* Cleaner abstractions for maintenance
* Explore streaming for large files (beyond disk/RAM capacity)

![](https://camo.githubusercontent.com/61c169ab5cd01346bc3dc7a11edc1d218f0be3b4/68747470733a2f2f7062732e7477696d672e636f6d2f6d656469612f4447536344626c554941416f34482d2e6a70673a6c61726765)

## Watchdog modes:

History/context: the original watchdog supported mode the Serializing fork mode only and Afterburn was available for testing via a pull request.

When the of-watchdog is complete this version will support four modes as listed below. We may consolidate or remove some of these modes before going to 1.0 so please consider modes 2-4 experimental.


### 1. Serializing fork (mode=serializing)

#### 1.1 Status

This mode is designed to replicate the behaviour of the original watchdog for backwards compatibility.

#### 1.2 Description

Forks one process per request. Multi-threaded. Ideal for retro-fitting a CGI application handler i.e. for Flask.

Limited to processing files sized as per available memory.

Reads entire request into memory from the HTTP request. At this point we serialize or modify if required. That is then written into the stdin pipe.

* Stdout pipe is read into memory and then serialized or modified if necessary before being written back to the HTTP response.

* HTTP headers can be set even after executing the function.

* A static Content-type can be set ahead of time.

* Exec timeout: supported.

### 2. HTTP (mode=http)

#### 2.1 Status

The HTTP mode is set to become the default mode for future OpenFaaS templates.

The following templates have been available for testing:

| Template               | HTTP framework     | Repo                                                             |
|------------------------|--------------------|------------------------------------------------------------------|
| Node.js                | Express.js         | https://github.com/openfaas-incubator/node8-express-template     |
| Python 2.7             | Flask              | https://github.com/openfaas-incubator/python27-flask-template    |
| Golang                 | Go HTTP (stdlib )  | https://github.com/openfaas-incubator/golang-http-template       |

#### 2.2 Description

The HTTP mode is similar to AfterBurn.

A process is forked when the watchdog starts, we then forward any request incoming to the watchdog to a HTTP port within the container.

Pros:

* Fastest option - high concurrency and throughput

* Does not require new/custom client libraries like afterburn but makes use of a long-running daemon such as Express.js for Node or Flask for Python

Example usage for testing:

* Forward to an NGinx container:

```
$ go build ; mode=http port=8081 fprocess="docker run -p 80:80 --name nginx -t nginx" upstream_url=http://127.0.0.1:80 ./of-watchdog
```

* Forward to a Node.js / Express.js hello-world app:

```
$ go build ; mode=http port=8081 fprocess="node expressjs-hello-world.js" upstream_url=http://127.0.0.1:3000 ./of-watchdog
```

Cons:

* Questionable as to whether this is actually "serverless"

* Daemons such as express/flask/sinatra could be hard to configure or potentially unpredictable when used in this way

* One more HTTP hop in the chain between the client and the function

### 3. Streaming fork (mode=streaming) - default.

Forks a process per request and can deal with more data than is available memory capacity - i.e. 512mb VM can process multiple GB of video.

HTTP headers cannot be sent after function starts executing due to input/output being hooked-up directly to response for streaming efficiencies. Response code is always 200 unless there is an issue forking the process. An error mid-flight will have to be picked up on the client. Multi-threaded.

* Input is sent back to client as soon as it's printed to stdout by the executing process.

* A static Content-type can be set ahead of time.

* Exec timeout: supported.

### 4. Afterburn (mode=afterburn)

### 4.1 Status

Afterburn should be considered for deprecation in favour of the HTTP mode.

Several sample templates are available under the OpenFaaS incubator organisation.

https://github.com/openfaas/nodejs-afterburn

https://github.com/openfaas/python-afterburn

https://github.com/openfaas/java-afterburn

### 4.2 Details

Uses a single process for all requests, if that request dies the container dies.

Vastly accelerated processing speed but requires a client library for each language - HTTP over stdin/stdout. Single-threaded with a mutex.

* Limited to processing files sized as per available memory.

* HTTP headers can be set even after executing the function.

* A dynamic Content-type can be set from the client library.

* Exec timeout: not supported.

## Configuration

Environmental variables:

| Option                 | Implemented | Usage             |
|------------------------|--------------|-------------------------------|
| `function_process`     | Yes          | The process to invoke for each function call function process (alias - fprocess). This must be a UNIX binary and accept input via STDIN and output via STDOUT.  |
| `read_timeout`         | Yes          | HTTP timeout for reading the payload from the client caller (in seconds) |
| `write_timeout`        | Yes          | HTTP timeout for writing a response body from your function (in seconds)  |
| `exec_timeout`         | Yes          | Exec timeout for process exec'd for each incoming request (in seconds). Disabled if set to 0. |
| `port`                 | Yes          | Specify an alternative TCP port for testing |
| `write_debug`          | No           | Write all output, error messages, and additional information to the logs. Default is false. |
| `content_type`         | Yes          | Force a specific Content-Type response for all responses - only in forking/serializing modes. |
| `suppress_lock`        | Yes           | The watchdog will attempt to write a lockfile to /tmp/ for swarm healthchecks - set this to true to disable behaviour. |
| `upstream_url`         | Yes          | `http` mode only - where to forward requests i.e. 127.0.0.1:5000 |
