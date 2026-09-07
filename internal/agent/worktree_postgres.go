package agent

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// WorktreePostgres is retained authority, not an observation cache. Names alone
// never authorize a mutation; the resolver verifies these bindings against Docker
// and authenticates the recorded cluster before advertising an endpoint.
type WorktreePostgres struct {
	InstanceID      string                   `json:"instance_id"`
	DaemonID        string                   `json:"daemon_id"`
	DaemonEndpoint  string                   `json:"daemon_endpoint"`
	Image           string                   `json:"image"`
	Major           int                      `json:"major"`
	User            string                   `json:"user"`
	Password        string                   `json:"password"`
	Container       string                   `json:"container"`
	ContainerID     string                   `json:"container_id,omitempty"`
	Volume          string                   `json:"volume"`
	VolumeCreatedAt string                   `json:"volume_created_at,omitempty"`
	VolumeDriver    string                   `json:"volume_driver,omitempty"`
	VolumeMount     string                   `json:"volume_mount,omitempty"`
	SystemID        string                   `json:"system_id,omitempty"`
	Port            int                      `json:"port,omitempty"`
	Phase           string                   `json:"phase"`
	Restore         *WorktreePostgresRestore `json:"restore,omitempty"`
}

type WorktreePostgresRestore struct {
	ArchiveSHA256 string    `json:"archive_sha256"`
	Mode          string    `json:"mode"`
	StartedAt     time.Time `json:"started_at"`
	SQLStarted    bool      `json:"sql_started"`
}

const worktreePostgresDescriptor = `{"type":"object","required":["instance_id","daemon_id","daemon_endpoint","image","major","user","password","container","volume","phase"],"properties":{"instance_id":{"type":"string"},"daemon_id":{"type":"string"},"daemon_endpoint":{"type":"string"},"image":{"type":"string"},"major":{"type":"integer"},"user":{"type":"string"},"password":{"type":"string"},"container":{"type":"string"},"container_id":{"type":"string"},"volume":{"type":"string"},"volume_created_at":{"type":"string"},"volume_driver":{"type":"string"},"volume_mount":{"type":"string"},"system_id":{"type":"string"},"port":{"type":"integer"},"phase":{"enum":["pending","volume","container","ready","restoring","restore-failed","deleting"]},"restore":{"type":"object","required":["archive_sha256","mode","started_at","sql_started"],"properties":{"archive_sha256":{"type":"string"},"mode":{"enum":["overwrite","merge"]},"started_at":{"type":"string","format":"date-time"},"sql_started":{"type":"boolean"}},"additionalProperties":false}},"additionalProperties":false}`

func (p WorktreePostgres) validate() error {
	if len(p.InstanceID) != 32 || strings.Trim(p.InstanceID, "0123456789abcdef") != "" ||
		p.DaemonID == "" || !strings.HasPrefix(p.DaemonEndpoint, "unix:///") ||
		!strings.Contains(p.Image, "@sha256:") || p.Major < 1 || p.User == "" || p.Password == "" ||
		p.Container != "scenery-pg-"+p.InstanceID || p.Volume != "scenery-pg-data-"+p.InstanceID || p.Port < 0 || p.Port > 65535 {
		return fmt.Errorf("retained worktree PostgreSQL authority is incomplete or invalid; no resource was modified")
	}
	switch p.Phase {
	case "pending", "volume", "container", "ready", "restoring", "restore-failed", "deleting":
	default:
		return fmt.Errorf("retained worktree PostgreSQL phase is unsupported")
	}
	if p.Phase != "pending" && p.Phase != "deleting" && (p.VolumeCreatedAt == "" || p.VolumeDriver == "" || p.VolumeMount == "") {
		return fmt.Errorf("retained worktree PostgreSQL volume identity is missing")
	}
	if p.Phase == "ready" && (p.ContainerID == "" || p.SystemID == "" || p.Port == 0) {
		return fmt.Errorf("retained ready PostgreSQL resource has incomplete identity")
	}
	if p.Restore != nil {
		digest, err := hex.DecodeString(strings.TrimPrefix(p.Restore.ArchiveSHA256, "sha256:"))
		if err != nil || len(digest) != 32 || !strings.HasPrefix(p.Restore.ArchiveSHA256, "sha256:") || p.Restore.StartedAt.IsZero() || (p.Restore.Mode != "overwrite" && p.Restore.Mode != "merge") {
			return fmt.Errorf("retained PostgreSQL restore identity is incomplete")
		}
	}
	return nil
}

func validatePostgresUpdate(previous, next *WorktreePostgres) error {
	if next != nil {
		if err := next.validate(); err != nil {
			return err
		}
	}
	if previous == nil {
		return nil
	}
	if next == nil || previous.InstanceID != next.InstanceID || previous.DaemonID != next.DaemonID ||
		previous.DaemonEndpoint != next.DaemonEndpoint || previous.Image != next.Image || previous.Major != next.Major ||
		previous.User != next.User || previous.Password != next.Password || previous.Container != next.Container || previous.Volume != next.Volume {
		return fmt.Errorf("retained PostgreSQL authority cannot be replaced or removed before verified resource retirement")
	}
	if (previous.VolumeCreatedAt != "" && previous.VolumeCreatedAt != next.VolumeCreatedAt) ||
		(previous.VolumeDriver != "" && previous.VolumeDriver != next.VolumeDriver) ||
		(previous.VolumeMount != "" && previous.VolumeMount != next.VolumeMount) ||
		(previous.SystemID != "" && previous.SystemID != next.SystemID) {
		return fmt.Errorf("retained PostgreSQL data identity cannot be replaced")
	}
	if prior := previous.Restore; prior != nil {
		if current := next.Restore; current != nil {
			if current.ArchiveSHA256 != prior.ArchiveSHA256 || current.Mode != prior.Mode || !current.StartedAt.Equal(prior.StartedAt) || (prior.SQLStarted && !current.SQLStarted) {
				return fmt.Errorf("pending PostgreSQL restore authority cannot be replaced")
			}
		} else if next.Phase != "ready" {
			return fmt.Errorf("pending PostgreSQL restore can clear only after successful completion")
		}
	}
	return nil
}
