package volume

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/getarcaneapp/arcane/backend/v2/internal/backup"
	"github.com/getarcaneapp/arcane/backend/v2/internal/database"
	volumetypes "github.com/getarcaneapp/arcane/types/v2/volume"
	"github.com/libtnb/sqlite"
	"github.com/stretchr/testify/require"
	"go.getarcane.app/sys/crypto"
	"gorm.io/gorm"
)

func TestVolumeBackupPasswordPrefersRecoveryKey(t *testing.T) {
	gormDB, err := gorm.Open(sqlite.Open("file:volume-backup-password?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gormDB.AutoMigrate(&backup.SystemBackupRecoveryConfig{}))
	crypto.InitEncryption(&crypto.Config{EncryptionKey: "volume-backup-password-test-key-32bytes", Environment: "test"})
	recoveryKeys := backup.NewRecoveryKeyStore(&database.DB{DB: gormDB})
	service := &VolumeService{
		db:            &database.DB{DB: gormDB},
		encryptionKey: "legacy-instance-secret",
		recoveryKeys:  recoveryKeys,
	}

	// Without a stored recovery key the legacy instance-key derivation applies.
	password, err := service.volumeBackupPasswordInternal(t.Context())
	require.NoError(t, err)
	require.Equal(t, service.legacyVolumePasswordInternal(), password)

	// Once a recovery key is stored it replaces the derived password.
	recoveryKey := "QWERTY-ABCDEF-234567-GHIJKL-MNOPQR-STUVWX-YZ2345-ZXCVBN"
	require.NoError(t, recoveryKeys.Set(t.Context(), recoveryKey))
	password, err = service.volumeBackupPasswordInternal(t.Context())
	require.NoError(t, err)
	require.Equal(t, recoveryKey, password)
}

func TestDiscoveredVolumeBackupMapping(t *testing.T) {
	snapshot := backup.DiscoveredSnapshot{ID: "abc123", Label: "app-data", Time: time.Unix(1700000000, 0).UTC()}
	snapshot.Summary.TotalBytesProcessed = 2048

	entry := discoveredVolumeBackupInternal("destination-1", "instance-a", snapshot)
	require.NotNil(t, entry)
	require.Equal(t, "app-data", entry.VolumeName)
	require.Equal(t, "remote-destination-1-instance-a-abc123", entry.ID)
	require.Equal(t, VolumeBackupStatusSucceeded, entry.Status)
	require.Equal(t, VolumeBackupTriggerManual, entry.Trigger)
	require.Equal(t, volumetypes.BackupDestinationS3, entry.Destination)
	require.Equal(t, VolumeBackupFormatRustic, entry.Format)
	require.Equal(t, "instance-a", entry.RemoteInstanceID)
	require.Equal(t, "destination-1", entry.S3DestinationID)
	require.Equal(t, int64(2048), entry.Size)
	require.True(t, entry.CreatedAt.Equal(time.Unix(1700000000, 0).UTC()))

	// Snapshots without a volume label, and system recovery snapshots, never
	// map to a volume backup.
	require.Nil(t, discoveredVolumeBackupInternal("destination-1", "instance-a", backup.DiscoveredSnapshot{ID: "x"}))
	require.Nil(t, discoveredVolumeBackupInternal("destination-1", "instance-a", backup.DiscoveredSnapshot{ID: "x", Label: "arcane-system-recovery"}))

	// A missing snapshot timestamp falls back to now.
	fallback := discoveredVolumeBackupInternal("d", "r", backup.DiscoveredSnapshot{ID: "x", Label: "vol"})
	require.NotNil(t, fallback)
	require.False(t, fallback.CreatedAt.IsZero())
}

func TestDiscoveredSnapshotDecodesRusticLabel(t *testing.T) {
	// Rustic snapshot JSON carries the volume name in the label field written
	// by CreateSnapshot; discovery maps it back to the volume.
	payload := `{"id":"abc123","label":"app-data","time":"2026-09-08T10:00:00Z","summary":{"total_bytes_processed":2048}}`
	var snapshot backup.DiscoveredSnapshot
	require.NoError(t, json.Unmarshal([]byte(payload), &snapshot))
	require.Equal(t, "abc123", snapshot.ID)
	require.Equal(t, "app-data", snapshot.Label)
	require.Equal(t, int64(2048), snapshot.Summary.TotalBytesProcessed)
	require.False(t, snapshot.Time.IsZero())
}
