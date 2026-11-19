package awswire

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

// minPepperBytes mirrors apiserver: a shorter pepper is refused.
const minPepperBytes = 32

// ssmAPI is the one SSM call used; tests substitute a fake.
type ssmAPI interface {
	GetParameter(ctx context.Context, in *ssm.GetParameterInput, opts ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// LoadPepper reads the SecureString parameter name (standard base64 of at least 32 bytes) and decrypts it.
// Any problem is an error: the caller must fail closed, never run with auth weakened.
func LoadPepper(ctx context.Context, api ssmAPI, name string) ([]byte, error) {
	if name == "" {
		return nil, errors.New("PEPPER_PARAM is not set")
	}
	out, err := api.GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String(name), WithDecryption: aws.Bool(true)})
	if err != nil {
		return nil, fmt.Errorf("read pepper parameter: %w", err)
	}
	if out.Parameter == nil || out.Parameter.Value == nil {
		return nil, errors.New("pepper parameter has no value")
	}
	raw, err := base64.StdEncoding.DecodeString(*out.Parameter.Value)
	if err != nil {
		return nil, errors.New("pepper parameter is not valid base64") // never include the value
	}
	if len(raw) < minPepperBytes {
		return nil, fmt.Errorf("pepper parameter holds %d bytes, need at least %d", len(raw), minPepperBytes)
	}
	return raw, nil
}
