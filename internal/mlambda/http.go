package mlambda

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	jsonv2 "github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"
)

// https://docs.aws.amazon.com/apigateway/latest/developerguide/http-api-develop-integrations-lambda.html
func HttpHandler(h http.Handler) Handler {
	return HandlerFunc(func(ctx context.Context, w io.Writer, r *Request) error {

		var proxyRequest httpRequest
		err := jsonv2.UnmarshalRead(r.Body, &proxyRequest)
		if err != nil {
			return err
		}

		body := []byte(proxyRequest.Body)
		if proxyRequest.IsBase64Encoded {
			body, err = base64.RawStdEncoding.DecodeString(proxyRequest.Body)
			if err != nil {
				return err
			}
		}

		var httpReq http.Request
		httpReq.Header = http.Header{}

		httpReq.Body = io.NopCloser(bytes.NewReader(body))

		// RawPath + RawQueryString
		urlStr := proxyRequest.RawPath
		if proxyRequest.RawQueryString != "" {
			urlStr = urlStr + "?" + proxyRequest.RawQueryString
		}
		if urlStr != "" {
			parsedUrl, err := url.ParseRequestURI(urlStr)
			if err != nil {
				return fmt.Errorf("parsing rawpath and rawquery: %s", err)
			}
			httpReq.URL = parsedUrl
			httpReq.RequestURI = urlStr
		} else {
			// ?
			httpReq.URL = &url.URL{}
		}

		// Cookies
		// these may get over-ridden by the headers?
		cookieStr := strings.Join(proxyRequest.Cookies, "; ")
		if cookieStr != "" {
			httpReq.Header.Set("Cookie", cookieStr)
		}

		// User Agent
		// may get over-ridden in main header-loop
		httpReq.Header.Set("User-Agent", proxyRequest.RequestContext.Http.UserAgent)

		// Headers
		// lambda concatenates headers for some reason - we
		// do not try to un-concat them
		for k, v := range proxyRequest.Headers {
			httpReq.Header.Set(k, v)
		}

		// Query String Parameters
		// nothing to do - Go parses them from the RawQueryString

		// Domain name -> Host
		httpReq.Host = proxyRequest.RequestContext.DomainName

		// Method
		httpReq.Method = proxyRequest.RequestContext.Http.Method

		// Path
		// nothing to do

		// Protocol
		httpReq.Proto = proxyRequest.RequestContext.Http.Protocol

		// Source IP
		// nothing to do

		// Path parameters
		// nothing to do

		// Set raw request struct in context?

		rw := responseWriter{w: w, header: http.Header{}}
		h.ServeHTTP(&rw, &httpReq)
		rw.finish()
		return nil
	})
}

type httpRequest struct {
	Version               string             `json:"version"`
	RoutKey               string             `json:"routeKey"`
	RawPath               string             `json:"rawPath"`
	RawQueryString        string             `json:"rawQueryString"`
	Cookies               []string           `json:"cookies"`
	Headers               map[string]string  `json:"headers"`
	QueryStringParameters map[string]string  `json:"queryStringParameters"`
	RequestContext        httpRequestContext `json:"requestContext"`
	Body                  string             `json:"body"`
	PathParameters        map[string]string  `json:"pathParameters"`
	IsBase64Encoded       bool               `json:"isBase64Encoded"`
	StageVariables        map[string]string  `json:"stageVariables"`
}

type httpRequestContext struct {
	AccountID      string          `json:"accountId"`
	ApiID          string          `json:"apiId"`
	Authentication json.RawMessage `json:"authentication"`
	Authorizer     json.RawMessage `json:"authorizer"`
	DomainName     string          `json:"domainName"`
	DomainPrefix   string          `json:"domainPrefix"`
	Http           struct {
		Method    string `json:"method"`
		Path      string `json:"path"`
		Protocol  string `json:"protocol"`
		SourceIP  string `json:"sourceIp"`
		UserAgent string `json:"userAgent"`
	} `json:"http"`
	RequestID string `json:"requestId"`
	RouteKey  string `json:"routeKey"`
	Stage     string `json:"stage"`
	Time      string `json:"time"`
	TimeEpoch int64  `json:"timeEpoch"`
}

type responseWriter struct {
	mu          sync.Mutex
	w           io.Writer
	body        io.WriteCloser
	sentHeaders bool
	header      http.Header
}

// Header implements http.ResponseWriter.
func (r *responseWriter) Header() http.Header {
	return r.header
}

// Write implements http.ResponseWriter.
func (r *responseWriter) Write(p []byte) (int, error) {
	r.mu.Lock()
	r.sendHeaders(200)
	len, err := r.body.Write(p)
	r.mu.Unlock()
	return len, err
}

// WriteHeader implements http.ResponseWriter.
func (r *responseWriter) WriteHeader(statusCode int) {
	r.mu.Lock()
	r.sendHeaders(statusCode)
	r.mu.Unlock()
}

func (r *responseWriter) sendHeaders(statusCode int) {
	if r.sentHeaders {
		return
	}
	r.sentHeaders = true

	// manually construct JSON response, leaving a "spot"
	// for the streaming body
	var dst []byte
	dst = append(dst, []byte("{")...)

	dst, _ = jsontext.AppendQuote(dst, "isBase64Encoded")
	dst = append(dst, []byte(":")...)
	dst = append(dst, []byte(jsontext.Bool(true).String())...)
	dst = append(dst, []byte(",")...)

	dst, _ = jsontext.AppendQuote(dst, "statusCode")
	dst = append(dst, []byte(":")...)
	dst = append(dst, []byte(jsontext.Int(int64(statusCode)).String())...)
	dst = append(dst, []byte(",")...)

	// cookies
	cs := r.header.Values("set-cookie")
	r.header.Del("set-cookie")
	if len(cs) > 0 {
		dst, _ = jsontext.AppendQuote(dst, "cookies")
		dst = append(dst, []byte(":[")...)
		for i, c := range cs {
			if i > 0 {
				dst = append(dst, []byte(",")...)
			}
			dst, _ = jsontext.AppendQuote(dst, c)
		}
		dst = append(dst, []byte("],")...)
	}

	// headers
	if len(r.header) > 0 {
		dst, _ = jsontext.AppendQuote(dst, "multiValueHeaders")
		dst = append(dst, []byte(":{")...)

		var needsComma bool
		for k, vs := range r.header {
			if needsComma {
				dst = append(dst, []byte(",")...)
			}
			needsComma = true
			dst, _ = jsontext.AppendQuote(dst, k)
			dst = append(dst, []byte(":[")...)
			for i, v := range vs {
				if i > 0 {
					dst = append(dst, []byte(",")...)
				}
				dst, _ = jsontext.AppendQuote(dst, v)
			}
			dst = append(dst, []byte("]")...)
		}

		dst = append(dst, []byte("},")...)
	}

	// start 'body' prop, and open-quote for body-string
	dst, _ = jsontext.AppendQuote(dst, "body")
	dst = append(dst, []byte(":\"")...)

	// TODO - retry etc?
	r.w.Write(dst)

	// prep body-writer
	r.body = base64.NewEncoder(base64.StdEncoding, r.w)
}

func (r *responseWriter) finish() {
	r.sendHeaders(200)
	// flush body
	r.body.Close()

	// close body-string and response object
	r.w.Write([]byte("\"}"))
}

var _ http.ResponseWriter = (*responseWriter)(nil)
