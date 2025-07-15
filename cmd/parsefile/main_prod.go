//go:build lambda

package main

import _ "github.com/aws/aws-lambda-go/lambda"

// main is the production entrypoint invoked by AWS Lambda.
func main() {
	if err := run(); err != nil {
		panic(err)
	}
}
