// Command crimson-api-lambda is the public API on AWS Lambda behind an API Gateway HTTP API.
package main

import (
	"time"

	"github.com/aws/aws-lambda-go/lambda"

	"crimsonledger/internal/awswire"
)

func main() {
	env := awswire.EnvFromOS()
	lambda.Start(awswire.Public(awswire.InitAWS(env), awswire.Logger(), time.Now))
}
