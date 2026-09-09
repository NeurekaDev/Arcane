package volume

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"emperror.dev/errors"

	"github.com/getarcaneapp/arcane/backend/v2/internal/backup"
	"github.com/moby/moby/client"
)

// MigrateRepositoryPasswords re-keys this instance's volume backup
// repositories from the legacy instance-key derivation to the recovery key.
// It is idempotent: repositories already keyed by the recovery key are
// detected and skipped, and missing repositories are ignored. Only this
// instance's local repository and S3 root are migrated; other instances
// sharing a destination re-key their own roots on their own startup, so
// instances sharing a destination should be upgraded together.
func (s *VolumeService) MigrateRepositoryPasswords(ctx context.Context) error {
	if s.recoveryKeys == nil || s.engine == nil {
		return nil
	}
	key, err := s.recoveryKeys.Get(ctx)
	if errors.Is(err, backup.ErrRecoveryKeyNotConfigured) {
		slog.DebugContext(ctx, "volume backup re-key skipped: no recovery key configured")
		return nil
	}
	if err != nil {
		return err
	}
	dockerClient, err := s.dockerService.GetClient(ctx)
	if err != nil {
		return err
	}
	legacy := s.legacyVolumePasswordInternal()
	var rekeyErr error
	if err := s.rekeyVolumeRepositoryInternal(ctx, dockerClient, "volumes:local", func(readOnly bool) (backup.Repository, error) {
		return s.localRusticRepositoryInternal(ctx, dockerClient, readOnly)
	}, legacy, key); err != nil {
		rekeyErr = errors.Combine(rekeyErr, fmt.Errorf("local repository: %w", err))
	}
	if s.s3Destinations != nil {
		destinations, listErr := s.s3Destinations.ListS3DestinationsByID(ctx)
		if listErr != nil {
			return errors.Combine(rekeyErr, fmt.Errorf("failed to list S3 destinations for re-key: %w", listErr))
		}
		instanceID := strings.TrimSpace(s.settingsService.GetSettingsConfig().InstanceID.Value)
		if instanceID == "" {
			return errors.Combine(rekeyErr, errors.New("arcane instance ID is unavailable"))
		}
		for destinationID := range destinations {
			if err := s.rekeyVolumeRepositoryInternal(ctx, dockerClient, "volumes:s3:"+destinationID+":"+instanceID, func(bool) (backup.Repository, error) {
				return s.remoteRusticRepositoryForInstanceInternal(ctx, destinationID, instanceID)
			}, legacy, key); err != nil {
				rekeyErr = errors.Combine(rekeyErr, fmt.Errorf("destination %s: %w", destinationID, err))
			}
		}
	}
	return rekeyErr
}

// rekeyVolumeRepositoryInternal converts one repository when it still opens
// with the legacy password. A repository that already lists with the recovery
// key is skipped; one that cannot be opened at all (missing or foreign) is
// reported so the remaining repositories still migrate.
func (s *VolumeService) rekeyVolumeRepositoryInternal(ctx context.Context, dockerClient *client.Client, repositoryID string, openRepository func(bool) (backup.Repository, error), legacyPassword, recoveryKey string) error {
	repository, err := openRepository(true)
	if err != nil {
		return err
	}
	if _, listErr := s.engine.ListSnapshots(ctx, dockerClient, repository, recoveryKey); listErr == nil {
		slog.DebugContext(ctx, "volume backup repository already uses the recovery key", "repository", repositoryID)
		return nil
	}
	readWrite, err := openRepository(false)
	if err != nil {
		return err
	}
	if err := s.engine.ChangeRepositoryPassword(ctx, dockerClient, readWrite, legacyPassword, recoveryKey); err != nil {
		return err
	}
	slog.InfoContext(ctx, "Re-keyed volume backup repository to the recovery key", "repository", repositoryID)
	return nil
}
