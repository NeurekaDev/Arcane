package backup

import (
	"testing"

	"github.com/libtnb/sqlite"
	"github.com/stretchr/testify/require"
	"go.getarcane.app/sys/crypto"
	"gorm.io/gorm"

	"github.com/getarcaneapp/arcane/backend/v2/internal/database"
)

func TestRecoveryKeyStoreRoundTrip(t *testing.T) {
	gormDB, err := gorm.Open(sqlite.Open("file:recovery-key-store?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gormDB.AutoMigrate(&SystemBackupRecoveryConfig{}))
	crypto.InitEncryption(&crypto.Config{EncryptionKey: "recovery-key-store-test-key-32bytes", Environment: "test"})
	store := NewRecoveryKeyStore(&database.DB{DB: gormDB})

	configured, err := store.Configured(t.Context())
	require.NoError(t, err)
	require.False(t, configured)

	_, err = store.Get(t.Context())
	require.ErrorIs(t, err, ErrRecoveryKeyNotConfigured)

	key, err := GenerateRecoveryKey()
	require.NoError(t, err)
	require.NoError(t, store.Set(t.Context(), key))

	configured, err = store.Configured(t.Context())
	require.NoError(t, err)
	require.True(t, configured)

	stored, err := store.Get(t.Context())
	require.NoError(t, err)
	require.Equal(t, key, stored)
}

func TestRecoveryKeyValidation(t *testing.T) {
	require.NoError(t, ValidateRecoveryKey("QWERTY-ABCDEF-234567-GHIJKL-MNOPQR-STUVWX-YZ2345-ZXCVBN"))
	require.Error(t, ValidateRecoveryKey("too-short"))
	require.Error(t, ValidateRecoveryKey(""))
	require.Error(t, ValidateRecoveryKey("QWERTY-ABCDEF-234567-GHIJKL-MNOPQR-STUVWX-YZ2345-ZXCVBN-EXTRA"))

	key, err := GenerateRecoveryKey()
	require.NoError(t, err)
	require.NoError(t, ValidateRecoveryKey(key))
}
