package deployment

import (
	"context"
	"fmt"
	"time"
)

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
			return last, fmt.Errorf("rollout of %s/%s did not complete within %s", namespace, name, opts.Timeout)
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(opts.Poll):
		}
	}
}
