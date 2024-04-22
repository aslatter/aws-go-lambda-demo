package mlambda

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Request represents a single incoming lambda event.
type Request struct {
	Body io.Reader
}

type Handler interface {
	Invoke(ctx context.Context, w io.Writer, r *Request) error
}

type HandlerFunc func(ctx context.Context, w io.Writer, r *Request) error

// Invoke implements Handler.
func (h HandlerFunc) Invoke(ctx context.Context, w io.Writer, r *Request) error {
	return h(ctx, w, r)
}

var _ Handler = (HandlerFunc)(nil)

// Server receives lambda invocations, handles them with the supplied
// handler, and returns the handler's response.
type Server struct {
	Handler Handler
	client  *client
}

// Start process lambda invocations indefinitely.
func (s *Server) Start(ctx context.Context) error {
	c, err := newClientFromEnv()
	if err != nil {
		// run a local HTTP version of the lambda if we aren't
		// actually running in AWS.
		return s.serveLocal(ctx)
	}

	s.client = c

	// main loop
	for {
		select {
		case <-ctx.Done():
			// TODO - logging
			return nil
		default:
		}

		err := s.doWork(ctx)
		if err != nil {
			return err
		}
	}
}

func (s *Server) doWork(parentCtx context.Context) error {
	// request new work

	// no timeout
	req, err := s.client.nextInvocation(parentCtx)
	if err != nil {
		return err
	}
	defer func() {
		io.Copy(io.Discard, req.body)
		req.body.Close()
	}()

	var ctx context.Context
	var ctxDone func()

	if req.deadline.IsZero() {
		// this doesn't do much, but it does ensure that if there
		// is some control-flow bug in this code the handler-goroutine
		// will be running with a canceled context.
		ctx, ctxDone = context.WithCancel(parentCtx)
	} else {
		ctx, ctxDone = context.WithDeadline(parentCtx, req.deadline)
	}
	defer ctxDone()

	// This is the tricky bit. We want to offer a Writer
	// to the handler because it's a better interface, but
	// the lambda-response goes back to AWS in an HTTP request
	// body so we need a Reader.
	//
	// The answer is to use `io.Pipe()`. As soon as the handler
	// send any data down its half of the pipe we send off the
	// response to the Lambda service.

	pipeReader, pipeWriter := io.Pipe()
	defer func() {
		pipeReader.Close()
		pipeWriter.Close()
	}()

	go func() {
		err := s.Handler.Invoke(ctx, pipeWriter, &Request{
			Body: req.body,
		})
		if err != nil {
			// signal the reader something abnormal happened
			// (and stop our waiter from waiting ...)
			//
			// if we've already started sending the request back
			// to the lambda service this still gets reported as
			// an error, but the message is not obvious.
			//
			// I'm not sure what this looks like for a streaming
			// response lambda - hopefully an incomplete chunk-stream
			// (or stream-rst).
			pipeWriter.CloseWithError(err)
		} else {
			// normal exit - signal EOF
			pipeWriter.Close()
		}
	}()

	// wait for the handler to start writing data.
	// once it has done so, start sending the response
	// back up.
	bufReader := bufio.NewReader(pipeReader)
	_, err = bufReader.Peek(1)
	if err != nil && !errors.Is(err, io.EOF) {
		// TODO - do something with error?
		_ = s.client.invocationError(parentCtx, errorOptions{
			requestId:    req.id,
			errorType:    "Handler.Error",
			errorMessage: err.Error(),
		})
		return nil
	}

	// the app-handler has started producing a response, so
	// we're going to start sending it up.
	//
	// at this point, if the handler returns an error after
	// we get this far the reader we pass to the Go http
	// client will return that error from its 'Read' method,
	// which results in either:
	// * and incomplete chunk-encoded payload
	// * a content-length which is mis-matched from the bytes
	//   sent
	// either of which should be treated as an error by whatever
	// is receiving the payload.
	//
	// TODO - do something with error-return?
	_ = s.client.invocationResponse(parentCtx, responseOptions{
		requestId: req.id,
		body:      bufReader,
	})

	return nil
}

// serveLocal runs the handler on an HTTP-server on localhost. It is intended
// for testing out the handler locally.
func (s *Server) serveLocal(ctx context.Context) error {
	addr := "localhost:8080"
	fmt.Println("Serving lambda on ", addr)

	srv := &http.Server{
		Addr: addr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// serve lambda-handler as an http-handler
			wrapper := &writerWrapper{w: w}
			err := s.Handler.Invoke(r.Context(), wrapper, &Request{Body: r.Body})
			if err == nil {
				return
			}

			if !wrapper.didWrite {
				// return 500 if the handler hasn't started writing the response yet
				w.WriteHeader(500)
				fmt.Fprintln(w, err)
				return
			}

			// otherwise signal to the http package to close the response
			// uncleanly, so the caller at least knows something went wrong
			panic(http.ErrAbortHandler)
		}),
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, close := context.WithTimeout(context.Background(), 5*time.Second)
		defer close()
		srv.Shutdown(shutdownCtx)
	}()

	return srv.ListenAndServe()
}

type writerWrapper struct {
	w        io.Writer
	didWrite bool
}

// Write implements io.Writer.
func (w *writerWrapper) Write(p []byte) (n int, err error) {
	w.didWrite = true
	return w.w.Write(p)
}

var _ io.Writer = (*writerWrapper)(nil)
