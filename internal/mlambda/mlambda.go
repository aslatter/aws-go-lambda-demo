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

type Request struct {
	Body io.Reader
}

type Server struct {
	Handler func(ctx context.Context, response io.Writer, request *Request) error

	client *client
}

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

	ctx, ctxDone := context.WithCancel(parentCtx)
	defer ctxDone()
	// TODO - add deadline

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
		err := s.Handler(ctx, pipeWriter, &Request{
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
	// we're going to start sending it up
	// TODO - do something with error
	_ = s.client.invocationResponse(parentCtx, responseOptions{
		requestId: req.id,
		body:      bufReader,
	})

	return nil
}

// serveLocal runs the handler on an HTTP-server on localhost. It is intended
// for testing out the program locally.
func (s *Server) serveLocal(ctx context.Context) error {
	addr := "localhost:8080"
	fmt.Println("Serving lambda on ", addr)

	srv := &http.Server{
		Addr: addr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			wrapper := &writerWrapper{w: w}
			err := s.Handler(r.Context(), wrapper, &Request{Body: r.Body})
			if err != nil && !wrapper.didWrite {
				w.WriteHeader(500)
				fmt.Fprintln(w, err)
			}
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
