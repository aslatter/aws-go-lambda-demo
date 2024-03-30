package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/aslatter/aws-go-lambda-demo/internal/mlambda"

	"golang.org/x/sys/unix"
)

func main() {
	err := mainErr()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func mainErr() error {
	ctx, close := signal.NotifyContext(context.Background(), unix.SIGINT, unix.SIGTERM)
	defer close()

	s := mlambda.Server{
		Handler: func(ctx context.Context, w io.Writer, request *mlambda.Request) error {
			fmt.Fprintln(w, "PONG")
			io.Copy(w, request.Body)
			return nil
		},
	}

	return s.Start(ctx)
}