package transfer

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	scryptWorkFactor = 10 // keep the tests fast; production uses 18
}

const testPassphrase = "ABCD-EFGH-IJKL-MNOP-QRST-UVWX-YZ23-4567"

var testKeys = Keys{
	EncryptionKey:    "data-encryption-key-0123456789abcdef",
	BackupRecipients: "age1qyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqs3290gq",
	BackupIdentity:   "AGE-SECRET-KEY-1TESTTESTTESTTESTTESTTESTTESTTESTTESTTESTTESTTESTTESTTEST",
}

func exported(t *testing.T, keys Keys, passphrase string) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, ExportKeys(&buf, keys, passphrase))
	return buf.Bytes()
}

// V4-43: the two keys travel through an explicit, protected export.
func TestKeys_RoundTrip(t *testing.T) {
	file := exported(t, testKeys, testPassphrase)

	got, err := ImportKeys(bytes.NewReader(file), testPassphrase)
	require.NoError(t, err)
	assert.Equal(t, testKeys.EncryptionKey, got.EncryptionKey)
	assert.Equal(t, testKeys.BackupRecipients, got.BackupRecipients)
	assert.Equal(t, testKeys.BackupIdentity, got.BackupIdentity)
	assert.Equal(t, KeyFileFormat, got.Format)
	assert.False(t, got.CreatedAt.IsZero())
}

// Protected: the file gives nothing away without the passphrase.
func TestKeys_FileRevealsNothing(t *testing.T) {
	file := exported(t, testKeys, testPassphrase)
	for _, secret := range []string{testKeys.EncryptionKey, testKeys.BackupIdentity, "encryption_key", "AGE-SECRET-KEY"} {
		assert.NotContains(t, string(file), secret)
	}
}

func TestKeys_WrongPassphraseOrDamage(t *testing.T) {
	file := exported(t, testKeys, testPassphrase)

	_, err := ImportKeys(bytes.NewReader(file), "ZZZZ-ZZZZ-ZZZZ-ZZZZ-ZZZZ-ZZZZ-ZZZZ-ZZZZ")
	require.ErrorContains(t, err, "does not open this key file")

	damaged := append([]byte(nil), file...)
	damaged[len(damaged)-3] ^= 0xff
	_, err = ImportKeys(bytes.NewReader(damaged), testPassphrase)
	require.Error(t, err)

	_, err = ImportKeys(bytes.NewReader(file[:len(file)/2]), testPassphrase)
	require.Error(t, err)

	_, err = ImportKeys(strings.NewReader("not a key file at all"), testPassphrase)
	require.Error(t, err)

	_, err = ImportKeys(bytes.NewReader(file), "")
	require.Error(t, err)
}

// A passphrase read aloud and typed back may come in lower case, with spaces or a line end.
func TestKeys_PassphraseTypedBackLoosely(t *testing.T) {
	file := exported(t, testKeys, testPassphrase)
	for _, typed := range []string{
		strings.ToLower(testPassphrase),
		testPassphrase + "\r\n",
		"  " + strings.ReplaceAll(testPassphrase, "-", "- ") + " ",
	} {
		_, err := ImportKeys(bytes.NewReader(file), typed)
		require.NoError(t, err, "typed as %q", typed)
	}
}

func TestExportKeys_RefusesWeakInput(t *testing.T) {
	var buf bytes.Buffer
	require.ErrorContains(t, ExportKeys(&buf, Keys{}, testPassphrase), "no ENCRYPTION_KEY")
	require.ErrorContains(t, ExportKeys(&buf, testKeys, "short"), "too short")
	assert.Zero(t, buf.Len(), "nothing may be written when the export is refused")
}

func TestGeneratePassphrase(t *testing.T) {
	shape := regexp.MustCompile(`^([A-Z2-7]{4}-){7}[A-Z2-7]{4}$`)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		p, err := GeneratePassphrase()
		require.NoError(t, err)
		assert.Regexp(t, shape, p)
		assert.False(t, seen[p], "passphrases must not repeat")
		seen[p] = true
	}
	// A generated passphrase is accepted by the export.
	p, err := GeneratePassphrase()
	require.NoError(t, err)
	got, err := ImportKeys(bytes.NewReader(exported(t, testKeys, p)), p)
	require.NoError(t, err)
	assert.Equal(t, testKeys.EncryptionKey, got.EncryptionKey)
}

func TestKeys_EnvLines(t *testing.T) {
	lines := string(testKeys.EnvLines("/backups/backup-key.txt"))
	assert.Contains(t, lines, "ENCRYPTION_KEY="+testKeys.EncryptionKey+"\n")
	assert.Contains(t, lines, "BACKUP_AGE_RECIPIENTS="+testKeys.BackupRecipients+"\n")
	assert.Contains(t, lines, "# BACKUP_AGE_IDENTITY_FILE=/backups/backup-key.txt", "the identity setting is a commented hint, not switched on")
	assert.NotContains(t, lines, testKeys.BackupIdentity, "the private backup key never goes into the settings lines")

	bare := string(Keys{EncryptionKey: "k"}.EnvLines(""))
	assert.NotContains(t, bare, "BACKUP_AGE")
}
