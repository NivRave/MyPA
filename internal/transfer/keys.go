package transfer

import (
	"bytes"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"filippo.io/age"
)

// Two keys have to travel with a backup (V4-43): the data encryption key, without which the
// secrets inside the restored database cannot be opened, and the backup key, without which the
// backup itself cannot. They are exported explicitly, into a file protected by a passphrase, and
// never as part of a backup set: a set and the keys that open it must not sit in one file.

// KeyFileFormat is the version of the exported key file's layout.
const KeyFileFormat = 1

// scryptWorkFactor is age's cost parameter for the passphrase (2^n). Tests lower it.
var scryptWorkFactor = 18

// Keys is what the key file carries.
type Keys struct {
	Format    int       `json:"format"`
	CreatedAt time.Time `json:"created_at"`
	// EncryptionKey is ENCRYPTION_KEY: the data encryption key.
	EncryptionKey string `json:"encryption_key"`
	// BackupRecipients is BACKUP_AGE_RECIPIENTS: the public half of the backup key, so the new
	// install keeps encrypting its backups to the same key.
	BackupRecipients string `json:"backup_recipients,omitempty"`
	// BackupIdentity is the private half of the backup key (the contents of the file named by
	// BACKUP_AGE_IDENTITY_FILE). It is included only when the operator asks for it.
	BackupIdentity string `json:"backup_identity,omitempty"`
}

// GeneratePassphrase returns a random passphrase with 160 bits of entropy, in groups that are
// easy to read aloud and type: eight groups of four characters from A-Z and 2-7.
func GeneratePassphrase() (string, error) {
	raw := make([]byte, 20)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", fmt.Errorf("cannot generate a passphrase: %w", err)
	}
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw) // 32 characters
	groups := make([]string, 0, 8)
	for i := 0; i < len(encoded); i += 4 {
		groups = append(groups, encoded[i:i+4])
	}
	return strings.Join(groups, "-"), nil
}

// NormalizePassphrase undoes harmless differences in how a passphrase was typed back: spaces,
// line ends and letter case.
func NormalizePassphrase(p string) string {
	return strings.ToUpper(strings.Join(strings.Fields(p), ""))
}

// ExportKeys writes keys to w, encrypted with the passphrase (age, scrypt).
func ExportKeys(w io.Writer, keys Keys, passphrase string) error {
	if strings.TrimSpace(keys.EncryptionKey) == "" {
		return errors.New("there is no ENCRYPTION_KEY to export")
	}
	passphrase = NormalizePassphrase(passphrase)
	if len(passphrase) < 16 {
		return errors.New("the passphrase is too short to protect the keys")
	}
	keys.Format = KeyFileFormat
	if keys.CreatedAt.IsZero() {
		keys.CreatedAt = time.Now().UTC().Truncate(time.Second)
	}
	body, err := json.Marshal(keys)
	if err != nil {
		return err
	}
	recipient, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return fmt.Errorf("cannot use the passphrase: %w", err)
	}
	recipient.SetWorkFactor(scryptWorkFactor)
	enc, err := age.Encrypt(w, recipient)
	if err != nil {
		return fmt.Errorf("cannot start encrypting the keys: %w", err)
	}
	if _, err := enc.Write(body); err != nil {
		return fmt.Errorf("cannot encrypt the keys: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("cannot finish encrypting the keys: %w", err)
	}
	return nil
}

// ImportKeys reads a key file written by ExportKeys. A wrong passphrase and a damaged file give
// the same error: the file cannot be told apart from noise without the passphrase.
func ImportKeys(r io.Reader, passphrase string) (Keys, error) {
	identity, err := age.NewScryptIdentity(NormalizePassphrase(passphrase))
	if err != nil {
		return Keys{}, errors.New("the passphrase does not open this key file, or the file is damaged")
	}
	plain, err := age.Decrypt(r, identity)
	if err != nil {
		return Keys{}, errors.New("the passphrase does not open this key file, or the file is damaged")
	}
	body, err := io.ReadAll(io.LimitReader(plain, 1<<20))
	if err != nil {
		return Keys{}, errors.New("the passphrase does not open this key file, or the file is damaged")
	}
	var keys Keys
	if err := json.Unmarshal(body, &keys); err != nil {
		return Keys{}, errors.New("the key file was opened but its contents are not a MyPA key export")
	}
	if keys.Format != KeyFileFormat {
		return Keys{}, fmt.Errorf("key file format %d is not supported by this release (it reads format %d)", keys.Format, KeyFileFormat)
	}
	if strings.TrimSpace(keys.EncryptionKey) == "" {
		return Keys{}, errors.New("the key file holds no ENCRYPTION_KEY")
	}
	return keys, nil
}

// EnvLines renders the keys as lines for the install's settings file. identityFile is where the
// operator saved the backup identity (see BackupIdentity); empty when it was not exported.
func (k Keys) EnvLines(identityFile string) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# MyPA keys exported %s. Copy these lines into .env, then delete this file.\n", k.CreatedAt.Format("2006-01-02 15:04 MST"))
	fmt.Fprintf(&b, "ENCRYPTION_KEY=%s\n", k.EncryptionKey)
	if k.BackupRecipients != "" {
		fmt.Fprintf(&b, "BACKUP_AGE_RECIPIENTS=%s\n", k.BackupRecipients)
	}
	if identityFile != "" {
		fmt.Fprintf(&b, "# The backup key was written to the file below. Set this only while restoring, then remove it and move the file off this machine.\n")
		fmt.Fprintf(&b, "# BACKUP_AGE_IDENTITY_FILE=%s\n", identityFile)
	}
	return b.Bytes()
}
