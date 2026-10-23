package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/moureau-dev/menina/internal/core/cluster"
	"github.com/moureau-dev/menina/internal/core/config"
)

// VerificationResult is the durable outcome of declarative verification.
type VerificationResult struct {
	OK     bool      `json:"ok"`
	Checks []string  `json:"checks,omitempty"`
	Error  string    `json:"error,omitempty"`
	At     time.Time `json:"at"`
}

func (v VerificationResult) String() string {
	data, _ := json.Marshal(v)
	return string(data)
}

func (j *Jobs) SetTLS(terminate bool) {
	j.scheme = "http"
	if terminate {
		j.scheme = "https"
	}
}

// verifyRelease runs the app's declared checks after rollout. Reachability is
// not correctness: a release only becomes active after these pass.
func (j *Jobs) verifyRelease(ctx context.Context, app *config.ResolvedApp, image string) VerificationResult {
	result := VerificationResult{OK: true, At: time.Now().UTC()}
	spec := app.Verification
	if spec == nil || spec.Enabled == nil || !*spec.Enabled {
		return result
	}
	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	if spec.Path != "" {
		if err := j.verifyHTTP(ctx, app, spec, timeout); err != nil {
			result.OK = false
			result.Error = err.Error()
			return result
		}
		result.Checks = append(result.Checks, "http "+spec.Path)
	}
	if spec.SmokeTest != nil {
		name := k8s.SanitizeName(fmt.Sprintf("%s-verify-%d", app.Name, time.Now().Unix()))
		secretName := ""
		if len(app.Secrets) > 0 {
			var err error
			secretName, _, err = j.engine.EnsureAppSecrets(ctx, app)
			if err != nil {
				result.OK = false
				result.Error = err.Error()
				return result
			}
		}
		req := k8s.JobRequest{
			Namespace:     app.Namespace,
			Name:          name,
			Project:       app.Project,
			Environment:   app.Environment,
			App:           app.Name,
			Image:         image,
			Command:       spec.SmokeTest.Command,
			Env:           app.Env,
			SecretEnvName: secretName,
			SecretEnv:     map[string]string{},
			Timeout:       spec.SmokeTest.Timeout,
			PullPolicy:    app.PullPolicy,
		}
		for envName := range app.Secrets {
			req.SecretEnv[envName] = envName
		}
		res, err := j.cluster.RunJob(ctx, req)
		if err != nil {
			result.OK = false
			result.Error = fmt.Sprintf("smokeTest %s failed: %v", name, err)
			return result
		}
		if res.ExitCode != 0 {
			result.OK = false
			result.Error = fmt.Sprintf("smokeTest %s exited %d", name, res.ExitCode)
			return result
		}
		result.Checks = append(result.Checks, "smokeTest "+name)
	}
	return result
}

func (j *Jobs) verifyHTTP(ctx context.Context, app *config.ResolvedApp, spec *config.VerificationSpec, timeout time.Duration) error {
	if !app.Exposure.Enabled || len(app.Exposure.Hosts) == 0 {
		return fmt.Errorf("verify path %q requires HTTP exposure with hosts", spec.Path)
	}
	scheme := j.scheme
	if scheme == "" {
		scheme = "http"
	}
	method := spec.Method
	if method == "" {
		method = http.MethodGet
	}
	url := scheme + "://" + app.Exposure.Hosts[0] + spec.Path
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, method, url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if len(spec.ExpectStatus) > 0 {
		statusOK := false
		for _, want := range spec.ExpectStatus {
			if resp.StatusCode == want {
				statusOK = true
				break
			}
		}
		if !statusOK {
			return fmt.Errorf("%s %s returned HTTP %d, expected %v", method, url, resp.StatusCode, spec.ExpectStatus)
		}
	} else if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return fmt.Errorf("%s %s returned HTTP %d", method, url, resp.StatusCode)
	}
	if spec.ExpectBody != "" && !strings.Contains(string(body), spec.ExpectBody) {
		return fmt.Errorf("%s %s body does not contain %q", method, url, spec.ExpectBody)
	}
	for name, want := range spec.ExpectHeader {
		if got := resp.Header.Get(name); got != want {
			return fmt.Errorf("%s %s header %q is %q, expected %q", method, url, name, got, want)
		}
	}
	return nil
}
