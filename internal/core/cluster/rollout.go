package k8s

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrRolloutTimeout marks a rollout that did not complete before its deadline.
var ErrRolloutTimeout = errors.New("rollout timed out")

type RolloutOptions struct {
	Timeout time.Duration
	Poll    time.Duration
}

func WaitForRollout(ctx context.Context, cluster Cluster, namespace, name string, opts RolloutOptions) (*WorkloadStatus, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Minute
	}
	if opts.Poll <= 0 {
		opts.Poll = 2 * time.Second
	}
	deadline := time.Now().Add(opts.Timeout)
	var last *WorkloadStatus
	for {
		st, err := cluster.WorkloadStatus(ctx, namespace, name)
		if err != nil {
			return nil, err
		}
		last = st
		if st.Replicas > 0 && st.ReadyReplicas >= st.Replicas && st.UpdatedReplicas >= st.Replicas {
			return st, nil
		}
		if time.Now().After(deadline) {
			return last, fmt.Errorf("%w: %s/%s did not complete within %s", ErrRolloutTimeout, namespace, name, opts.Timeout)
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(opts.Poll):
		}
	}
}
