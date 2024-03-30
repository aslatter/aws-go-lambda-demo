# Microlambda

This project is a demo of working with a minimal OS-only
AWS Lambda function.

The *internal/mlambda* package is a micro SDK for an AWS
lambda runtime for Go.

The file *main.go* is an example of using the SDK for a basic
handler.

When run locally the handler will serve requests on localhost.

## Using in AWS

Run *just zip*. The file *bin/bootstrap.zip* can be used directly
as an "OS Only" lambda function (assuming you're running on a Linux
machine).
