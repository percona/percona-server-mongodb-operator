package s3

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
)

type CustomRetryer struct {
	// base is the AWS standard retryer.
	base aws.Retryer

	// MinBackoff is the minimum delay that will be returned from RetryDelay.
	MinBackoff time.Duration
}

func (r CustomRetryer) IsErrorRetryable(err error) bool {
	return r.base.IsErrorRetryable(err)
}

func (r CustomRetryer) MaxAttempts() int {
	return r.base.MaxAttempts()
}

func (r CustomRetryer) RetryDelay(attempt int, opErr error) (time.Duration, error) {
	delay, err := r.base.RetryDelay(attempt, opErr)
	if err != nil {
		return delay, err
	}

	if delay < r.MinBackoff {
		delay = r.MinBackoff
	}

	return delay, nil
}

func (r CustomRetryer) GetRetryToken(ctx context.Context, opErr error) (func(error) error, error) {
	return r.base.GetRetryToken(ctx, opErr)
}

func (r CustomRetryer) GetInitialToken() func(error) error {
	return r.base.GetInitialToken()
}

func NewCustomRetryer(numMaxRetries int, minBackoff, maxBackoff time.Duration) aws.Retryer {
	baseRetryer := retry.NewStandard(func(o *retry.StandardOptions) {
		// v2's MaxAttempts includes the first attempt, we add 1 to match v1 behavior
		o.MaxAttempts = numMaxRetries + 1
		o.MaxBackoff = maxBackoff
	})

	return &CustomRetryer{
		base:       baseRetryer,
		MinBackoff: minBackoff,
	}
}
