package volume

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"emperror.dev/errors"

	"github.com/getarcaneapp/arcane/backend/v2/internal/backup"
	s3utils "github.com/getarcaneapp/arcane/backend/v2/pkg/utils/s3"
	volumetypes "github.com/getarcaneapp/arcane/types/v2/volume"
)

// systemRecoverySnapshotLabel marks snapshots created by the system backup
// domain; they never belong to a volume.
const systemRecoverySnapshotLabel = "arcane-system-recovery"

// DiscoverRemoteBackups imports every snapshot found in the destination's
// per-instance volume backup repositories into the volume backup history.
// Snapshots are mapped back to their volume through the label Rustic records
// at creation time and remember which instance root they came from, so a
// fresh instance can restore backups pushed by another instance. Per-root
// failures (for example an unopenable legacy repository) are returned as
// messages without blocking the other roots.
func (s *VolumeService) DiscoverRemoteBackups(ctx context.Context, destinationID string) (int, []string, error) {
	if s.engine == nil || s.s3Destinations == nil || s.recoveryKeys == nil {
		return 0, nil, errors.New("volume backup discovery is unavailable")
	}
	key, err := s.recoveryKeys.Get(ctx)
	if errors.Is(err, backup.ErrRecoveryKeyNotConfigured) {
		return 0, nil, errors.New("configure a recovery key in system backups before discovering volume backups")
	}
	if err != nil {
		return 0, nil, err
	}
	configuration, err := s.s3Destinations.Configuration(ctx, destinationID)
	if err != nil {
		return 0, nil, errors.New("the selected S3 backup destination is not configured")
	}
	roots, err := s3utils.ListRepositoryRoots(ctx, configuration, "arcane-volume-backups")
	if err != nil {
		return 0, nil, fmt.Errorf("failed to list volume backup repositories: %w", err)
	}
	var knownIDs []string
	if err := s.db.WithContext(ctx).Model(&VolumeBackup{}).
		Where("s3_destination_id = ? AND remote_snapshot_id <> ''", destinationID).
		Pluck("remote_snapshot_id", &knownIDs).Error; err != nil {
		return 0, nil, err
	}
	known := make(map[string]struct{}, len(knownIDs))
	for _, id := range knownIDs {
		known[id] = struct{}{}
	}
	dockerClient, err := s.dockerService.GetClient(ctx)
	if err != nil {
		return 0, nil, err
	}
	created := 0
	var failures []string
	for _, root := range roots {
		repository, repoErr := s.remoteRusticRepositoryForInstanceInternal(ctx, destinationID, root)
		if repoErr != nil {
			failures = append(failures, fmt.Sprintf("instance %s: %v", root, repoErr))
			continue
		}
		snapshots, snapErr := s.engine.ListSnapshots(ctx, dockerClient, repository, key)
		if snapErr != nil {
			failures = append(failures, fmt.Sprintf("instance %s: %v", root, snapErr))
			continue
		}
		for _, snapshot := range snapshots {
			entry := discoveredVolumeBackupInternal(destinationID, root, snapshot)
			if entry == nil {
				continue
			}
			if _, exists := known[snapshot.ID]; exists {
				continue
			}
			known[snapshot.ID] = struct{}{}
			if err := s.db.WithContext(ctx).Create(entry).Error; err != nil {
				return created, failures, fmt.Errorf("failed to save discovered volume backup: %w", err)
			}
			created++
		}
	}
	if len(failures) > 0 {
		slog.WarnContext(ctx, "volume backup discovery completed with failures", "destination", destinationID, "created", created, "failedRoots", len(failures))
	}
	return created, failures, nil
}

// discoveredVolumeBackupInternal maps a remote snapshot to a volume backup
// record for the given destination and instance root. Snapshots without a
// volume label (or created by the system recovery repository) map to nothing.
func discoveredVolumeBackupInternal(destinationID, root string, snapshot backup.DiscoveredSnapshot) *VolumeBackup {
	volumeName := strings.TrimSpace(snapshot.Label)
	if volumeName == "" || volumeName == systemRecoverySnapshotLabel {
		return nil
	}
	createdAt := snapshot.Time
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	entry := &VolumeBackup{
		VolumeName: volumeName, Size: snapshot.Summary.TotalBytesProcessed, CreatedAt: createdAt,
		Status: VolumeBackupStatusSucceeded, Trigger: VolumeBackupTriggerManual,
		Destination: volumetypes.BackupDestinationS3, Format: VolumeBackupFormatRustic,
		RemoteSnapshotID: snapshot.ID, S3DestinationID: destinationID, RemoteInstanceID: root,
	}
	entry.ID = fmt.Sprintf("remote-%s-%s-%s", destinationID, root, snapshot.ID)
	return entry
}
