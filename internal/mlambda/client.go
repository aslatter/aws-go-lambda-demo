package mlambda

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"
)

//
// https://docs.aws.amazon.com/lambda/latest/dg/runtimes-api.html
//

const apiVersion = "2018-06-01"

type client struct {
	client   *http.Client
	endpoint string
}

func newClientFromEnv() (*client, error) {
	c := &client{
		client:   http.DefaultClient,
		endpoint: os.Getenv("AWS_LAMBDA_RUNTIME_API"),
	}
	if c.endpoint == "" {
		return nil, fmt.Errorf("AWS_LAMBDA_RUNTIME_API not set")
	}
	return c, nil
}

type request struct {
	body               io.ReadCloser
	id                 string
	deadline           time.Time
	invokedFunctionArn string
	traceId            string
	clientContext      string
	cognitoIdentity    string
}

func (c *client) nextInvocation(ctx context.Context) (*request, error) {
	url := "http://" + c.endpoint + "/" + apiVersion + "/runtime/invocation/next"

	httpRequest, err := http.NewRequestWithContext(ctx, "GET", url, http.NoBody)
	if err != nil {
		return nil, err
	}

	response, err := c.client.Do(httpRequest)
	if err != nil {
		return nil, err
	}

	if response.StatusCode/100 != 2 {
		response.Body.Close()
		return nil, fmt.Errorf("unexpected 'next' http-response: %v: %s", response.StatusCode, response.Status)
	}

	headers := response.Header

	var r request
	r.body = response.Body
	r.id = headers.Get("Lambda-Runtime-Aws-Request-Id")

	deadlineMs, err := strconv.ParseInt(headers.Get("Lambda-Runtime-Deadline-Ms"), 10, 64)
	if err == nil {
		r.deadline = time.UnixMilli(deadlineMs)
	}

	r.invokedFunctionArn = headers.Get("Lambda-Runtime-Invoked-Function-Arn")
	r.traceId = headers.Get("Lambda-Runtime-Trace-Id")
	r.clientContext = headers.Get("Lambda-Runtime-Client-Context")
	r.cognitoIdentity = headers.Get("Lambda-Runtime-Cognito-Identity")

	return &r, nil
}

type responseOptions struct {
	requestId string
	body      io.Reader
}

func (c *client) invocationResponse(ctx context.Context, opts responseOptions) error {
	url := "http://" + c.endpoint + "/" + apiVersion + "/runtime/invocation/" + opts.requestId + "/response"
	httpRequest, err := http.NewRequestWithContext(ctx, "POST", url, opts.body)
	if err != nil {
		return err
	}

	httpResponse, err := c.client.Do(httpRequest)
	if err != nil {
		return err
	}

	_, _ = io.Copy(io.Discard, httpResponse.Body)
	_ = httpResponse.Body.Close()

	if httpResponse.StatusCode/100 != 2 {
		return fmt.Errorf("unexpected 'next' http-response: %v: %s", httpResponse.StatusCode, httpResponse.Status)
	}

	return nil
}

type errorOptions struct {
	requestId    string
	errorMessage string
	errorType    string
	stackTrace   []string
}

func (c *client) invocationError(ctx context.Context, opts errorOptions) error {
	var requestBody struct {
		ErrorMessage string   `json:"errorMessage"`
		ErrorType    string   `json:"errorType"`
		StackTrace   []string `json:"stackTrace,omitempty"`
	}

	requestBody.ErrorMessage = opts.errorMessage
	requestBody.ErrorType = opts.errorType
	requestBody.StackTrace = opts.stackTrace

	requestBytes, err := json.Marshal(&requestBody)
	if err != nil {
		return err
	}

	url := "http://" + c.endpoint + "/" + apiVersion + "/runtime/invocation/" + opts.requestId + "/error"
	httpRequest, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(requestBytes))
	if err != nil {
		return err
	}

	httpRequest.Header.Set("Lambda-Runtime-Function-Error-Type", opts.errorType)

	resp, err := c.client.Do(httpRequest)
	if err != nil {
		return err
	}

	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("unexpected http status %v: %s", resp.StatusCode, resp.Status)
	}

	return nil
}
