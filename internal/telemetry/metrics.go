// Package telemetry defines metric names and shared instrumentation constants.
package telemetry

// ContentType is the Prometheus text exposition content type.
const ContentType = "text/plain; version=0.0.4; charset=utf-8"

// Control-plane and edge metric names.
const (
	Up                     = "menina_up"
	SnapshotVersion        = "menina_snapshot_version"
	Routes                 = "menina_routes"
	ProxySnapshotReady     = "menina_proxy_snapshot_ready"
	Jobs                   = "menina_jobs"
	QueueDepth             = "menina_queue_depth"
	Releases               = "menina_releases"
	DeploySuccessTotal     = "menina_deploy_success_total"
	DeployFailureTotal     = "menina_deploy_failure_total"
	DeployDurationSum      = "menina_deploy_duration_seconds_sum"
	DeployDurationCount    = "menina_deploy_duration_seconds_count"
	AppReadyReplicas       = "menina_app_ready_replicas"
	AppDesiredReplicas     = "menina_app_desired_replicas"
	NodeDiskTotalBytes     = "menina_node_disk_total_bytes"
	NodeDiskAvailableBytes = "menina_node_disk_available_bytes"
)
