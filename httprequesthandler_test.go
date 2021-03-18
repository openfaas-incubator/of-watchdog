package main

import (
	"bytes"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openfaas-incubator/of-watchdog/config"
)

func TestHTTPRequestHandler_WatchdogPassesVerbToUpstream(t *testing.T) {

	verbs := []string{http.MethodGet, http.MethodPut, http.MethodPut, http.MethodDelete}

	wc := config.WatchdogConfig{
		FunctionProcess: "cat",
		ExecTimeout:     time.Duration(time.Second),
	}

	for _, verb := range verbs {
		t.Run(verb, func(t *testing.T) {

			actualVerb := ""
			upstreamServer := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					actualVerb = r.Method
					w.WriteHeader(http.StatusOK)
				}))
			wc.UpstreamURL = upstreamServer.URL
			defer upstreamServer.Close()

			recordHTTPRequest(t, wc, verb, nil, nil)

			if actualVerb != verb {
				t.Errorf("upstream received incorrect HTTP verb - want: %s, got: %s", verb, actualVerb)
			}
		})
	}
}

func TestHTTPRequestHandler_WatchdogPassesBodyToUpstream(t *testing.T) {
	expectedBody := []byte("openfaas rocks")
	actualBody := []byte("")
	upstreamServer := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				defer r.Body.Close()
			}
			if bodyBytes, err := ioutil.ReadAll(r.Body); err == nil {
				actualBody = bodyBytes
			}
		}))

	wc := config.WatchdogConfig{
		FunctionProcess: "cat",
		ExecTimeout:     time.Duration(time.Second),
		UpstreamURL:     upstreamServer.URL,
	}

	recordHTTPRequest(t, wc, http.MethodGet, bytes.NewBuffer(expectedBody), nil)

	if !bytes.Equal(actualBody, expectedBody) {
		t.Errorf("upstream received incorrect body - want: %s, got: %s", expectedBody, actualBody)
	}
}

func TestHTTPRequestHandler_WatchdogPassesHeaderToUpsteam(t *testing.T) {
	expectedContentType := "text/plain"
	actualContentType := ""
	upstreamServer := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			actualContentType = r.Header.Get("Content-Type")
		}))

	wc := config.WatchdogConfig{
		FunctionProcess: "cat",
		ExecTimeout:     time.Duration(time.Second),
		UpstreamURL:     upstreamServer.URL,
	}

	recordHTTPRequest(t, wc, http.MethodGet, nil, map[string]string{
		"Content-Type": expectedContentType,
	})

	if actualContentType != expectedContentType {
		t.Errorf("upstream received incorrect content-type - want: %s, got: %s", expectedContentType, actualContentType)
	}
}

func TestHTTPRequestHandler_WatchdogReceivesStatusFromUpstream(t *testing.T) {
	expectedStatus := http.StatusOK
	upstreamServer := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(expectedStatus)
		}))

	wc := config.WatchdogConfig{
		FunctionProcess: "cat",
		ExecTimeout:     time.Duration(time.Second),
		UpstreamURL:     upstreamServer.URL,
	}

	rr := recordHTTPRequest(t, wc, http.MethodGet, nil, nil)

	if status := rr.Code; status != expectedStatus {
		t.Errorf("handler returned wrong status code -  want: %v, got: %v", expectedStatus, status)
	}
}

func TestHTTPRequestHandler_WatchdogReceivesBodyFromUpstream(t *testing.T) {
	expectedBody := []byte("openfaas rocks")
	upstreamServer := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write(expectedBody)
			w.WriteHeader(http.StatusOK)
		}))

	wc := config.WatchdogConfig{
		FunctionProcess: "cat",
		ExecTimeout:     time.Duration(time.Second),
		UpstreamURL:     upstreamServer.URL,
	}

	rr := recordHTTPRequest(t, wc, http.MethodGet, nil, nil)

	if rr.Body == nil {
		t.Errorf("handler retured nil body: expected %s", string(expectedBody))
	}

	bodyBytes, bodyErr := ioutil.ReadAll(rr.Body)
	if bodyErr != nil {
		t.Fatal("unable to read data from body")
	}

	if !bytes.Equal(bodyBytes, expectedBody) {
		t.Errorf("handler returned incorrect body - want: %s, got: %s", expectedBody, bodyBytes)
	}
}

func TestHTTPRequestHandler_StatusGatewayTimeout_WhenExecTimeouts(t *testing.T) {

	upstreamServer := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(time.Millisecond * 10)
		}))
	defer upstreamServer.Close()

	wc := config.WatchdogConfig{
		FunctionProcess: "cat",
		ExecTimeout:     time.Duration(time.Millisecond),
		UpstreamURL:     upstreamServer.URL,
	}

	rr := recordHTTPRequest(t, wc, http.MethodGet, nil, nil)

	expectedStatus := http.StatusGatewayTimeout
	if status := rr.Code; status != expectedStatus {
		t.Errorf("handler returned wrong status code -  want: %v, got: %v", expectedStatus, status)
	}
}

func recordHTTPRequest(
	t *testing.T, wc config.WatchdogConfig, method string,
	body io.Reader, header map[string]string) *httptest.ResponseRecorder {

	req, err := http.NewRequest(method, "/", body)
	if header != nil {
		for k, v := range header {
			req.Header.Set(k, v)
		}
	}
	if err != nil {
		t.Fatal(err)
	}

	handler := makeHTTPRequestHandler(wc)
	rr := httptest.NewRecorder()
	handler(rr, req)

	return rr
}
