package lfs

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"strings"
	"time"

	"github.com/github/git-lfs/vendor/_nuts/github.com/rubyist/tracerx"
)

type transferStats struct {
	HeaderSize int
	BodySize   int
	Start      time.Time
	Stop       time.Time
}

type transfer struct {
	requestStats  *transferStats
	responseStats *transferStats
}

var (
	// TODO should use some locks
	transfers       = make(map[*http.Response]*transfer)
	transferBuckets = make(map[string][]*http.Response)
)

func LogTransfer(key string, res *http.Response) {
	transferBuckets[key] = append(transferBuckets[key], res)
}

type HttpClient struct {
	*http.Client
}

func (c *HttpClient) Do(req *http.Request) (*http.Response, error) {
	traceHttpRequest(req)

	crc := countingRequest(req)
	if req.Body != nil {
		// Only set the body if we have a body, but create the countingRequest
		// anyway to make using zeroed stats easier.
		req.Body = crc
	}

	start := time.Now()
	res, err := c.Client.Do(req)
	if err != nil {
		return res, err
	}

	resstats := &transferStats{Start: start}

	traceHttpResponse(res)

	// Pull request stats
	reqHeaderSize := 0
	dump, err := httputil.DumpRequest(req, false)
	if err == nil {
		reqHeaderSize = len(dump)
	}
	reqstats := &transferStats{HeaderSize: reqHeaderSize, BodySize: crc.Count}

	cresp := countingResponse(res)
	res.Body = cresp

	transfers[res] = &transfer{requestStats: reqstats, responseStats: resstats}

	return res, err
}

func DoHTTP(req *http.Request) (*http.Response, error) {
	res, err := Config.HttpClient().Do(req)
	if res == nil {
		res = &http.Response{StatusCode: 0, Header: make(http.Header), Request: req}
	}
	return res, err
}

func (c *Configuration) HttpClient() *HttpClient {
	if c.httpClient != nil {
		return c.httpClient
	}

	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		Dial: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).Dial,
		TLSHandshakeTimeout: 5 * time.Second,
	}

	sslVerify, _ := c.GitConfig("http.sslverify")
	if sslVerify == "false" || len(os.Getenv("GIT_SSL_NO_VERIFY")) > 0 {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	c.httpClient = &HttpClient{
		&http.Client{Transport: tr, CheckRedirect: checkRedirect},
	}

	return c.httpClient
}

func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 3 {
		return errors.New("stopped after 3 redirects")
	}

	oldest := via[0]
	for key, _ := range oldest.Header {
		if key == "Authorization" {
			if req.URL.Scheme != oldest.URL.Scheme || req.URL.Host != oldest.URL.Host {
				continue
			}
		}
		req.Header.Set(key, oldest.Header.Get(key))
	}

	tracerx.Printf("api: redirect %s %s to %s", oldest.Method, oldest.URL, req.URL)

	return nil
}

var tracedTypes = []string{"json", "text", "xml", "html"}

func traceHttpRequest(req *http.Request) {
	tracerx.Printf("HTTP: %s %s", req.Method, req.URL.String())

	if Config.isTracingHttp == false {
		return
	}

	dump, err := httputil.DumpRequest(req, false)
	if err != nil {
		return
	}

	scanner := bufio.NewScanner(bytes.NewBuffer(dump))
	for scanner.Scan() {
		fmt.Fprintf(os.Stderr, "> %s\n", scanner.Text())
	}
}

func traceHttpResponse(res *http.Response) {
	if res == nil {
		return
	}

	tracerx.Printf("HTTP: %d", res.StatusCode)

	if Config.isTracingHttp == false {
		return
	}

	dump, err := httputil.DumpResponse(res, false)
	if err != nil {
		return
	}

	scanner := bufio.NewScanner(bytes.NewBuffer(dump))
	for scanner.Scan() {
		fmt.Fprintf(os.Stderr, "< %s\n", scanner.Text())
	}
}

func countingRequest(req *http.Request) *countingReadCloser {
	return &countingReadCloser{request: req, ReadCloser: req.Body}
}

func countingResponse(res *http.Response) *countingReadCloser {
	return &countingReadCloser{response: res, ReadCloser: res.Body}
}

type countingReadCloser struct {
	Count    int
	request  *http.Request
	response *http.Response
	io.ReadCloser
}

func (c *countingReadCloser) Read(b []byte) (int, error) {
	n, err := c.ReadCloser.Read(b)
	if err != nil && err != io.EOF {
		return n, err
	}

	c.Count += n

	if Config.isTracingHttp {
		contentType := ""
		if c.response != nil { // Response, only print certain kinds of data
			contentType = strings.ToLower(strings.SplitN(c.response.Header.Get("Content-Type"), ";", 2)[0])
		} else {
			contentType = strings.ToLower(strings.SplitN(c.request.Header.Get("Content-Type"), ";", 2)[0])
		}

		for _, tracedType := range tracedTypes {
			if strings.Contains(contentType, tracedType) {
				fmt.Fprint(os.Stderr, string(b[0:n]))
			}
		}
	}

	if err == io.EOF && Config.isTracingHttp {
		// This transfer is done, we're checking it this way so we can also
		// catch transfers where the caller forgets to Close() the Body.
		fmt.Fprintln(os.Stderr, "")

		if c.response != nil {
			if transfer, ok := transfers[c.response]; ok {
				resHeaderSize := 0
				dump, err := httputil.DumpResponse(c.response, false)
				if err == nil {
					resHeaderSize = len(dump)
				}
				transfer.responseStats.HeaderSize = resHeaderSize
				transfer.responseStats.BodySize = c.Count
				transfer.responseStats.Stop = time.Now()
			}
		}
	}
	return n, err
}

func DumpHttpStats(o io.Writer) {
	if !Config.isTracingHttp {
		return
	}

	fmt.Fprint(o, "HTTP Transfer Stats\n\n")
	fmt.Fprintf(o, "Concurrent Transfers: %d, Batch: %v\n\n", Config.ConcurrentTransfers(), Config.BatchTransfer())
	totalTransfers := 0
	totalTime := int64(0)

	for key, vs := range transferBuckets {
		reqWireSize := 0
		resWireSize := 0
		reqHeaderSize := 0
		resHeaderSize := 0
		reqBodySize := 0
		resBodySize := 0
		keyTransfers := 0
		keyTime := int64(0) // nanoseconds

		for _, r := range vs {
			s := transfers[r]
			reqWireSize += s.requestStats.HeaderSize + s.requestStats.BodySize
			resWireSize += s.responseStats.HeaderSize + s.responseStats.BodySize

			reqHeaderSize += s.requestStats.HeaderSize
			resHeaderSize += s.responseStats.HeaderSize

			reqBodySize += s.requestStats.BodySize
			resBodySize += s.responseStats.BodySize

			totalTime += s.responseStats.Stop.Sub(s.responseStats.Start).Nanoseconds()
			totalTransfers++

			keyTime += s.responseStats.Stop.Sub(s.responseStats.Start).Nanoseconds()
			keyTransfers++
		}

		wireSize := reqWireSize + resWireSize

		fmt.Fprintf(o, "%s:\n", key)
		fmt.Fprintf(o, "\tTransfers: %d\n", keyTransfers)
		fmt.Fprintf(o, "\tTime: %.2fms\n", float64(keyTime)/1000000.0)
		fmt.Fprintf(o, "\tWire Data (Bytes): %d\n", wireSize)
		fmt.Fprintf(o, "\t\tRequest Size (Bytes): %d\n", reqWireSize)
		fmt.Fprintf(o, "\t\t\tHeaders: %d\n", reqHeaderSize)
		fmt.Fprintf(o, "\t\t\tBodies: %d\n", reqBodySize)
		fmt.Fprintf(o, "\t\tResponse Size (Bytes): %d\n", resWireSize)
		fmt.Fprintf(o, "\t\t\tHeaders: %d\n", resHeaderSize)
		fmt.Fprintf(o, "\t\t\tBodies: %d\n", resBodySize)
		fmt.Fprintf(o, "\tMean Wire Size: %d\n", wireSize/keyTransfers)
		fmt.Fprintf(o, "\t\tRequests: %.2f\n", reqWireSize/keyTransfers)
		fmt.Fprintf(o, "\t\tResponses: %.2f\n", resWireSize/keyTransfers)
		fmt.Fprintf(o, "\tMean Transfer Time: %.2fms\n", float64(keyTime)/float64(keyTransfers)/1000000.0)
		fmt.Fprintln(o, "")
	}

	fmt.Fprintf(o, "\nTotal Transfers: %d\n", totalTransfers)
	fmt.Fprintf(o, "Total Time: %.2fms\n", float64(totalTime)/1000000.0)

	fmt.Fprintln(o, "")
}
