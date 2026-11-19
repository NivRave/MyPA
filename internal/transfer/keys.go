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
// backup itself cannot. The private backup key is kept off the machine by the operator. The data
// encryption key is exported explicitly into a protected file: locked with the backup key by
// default, or with a generated passphrase. It is never part of a backup set: a set and the keys
// that open it must not sit in one file.

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

// ExportKeysFor writes keys to w, encrypted to the backup key (its public half, the age
// recipients in BACKUP_AGE_RECIPIENTS). The file then opens with the private backup key, which
// the operator already keeps off the machine and has to bring to a restore anyway: nothing new
// has to be carried, and no secret is shown or typed. The private backup key itself cannot be
// exported this way (it would be locked with itself).
func ExportKeysFor(w io.Writer, keys Keys, recipients []age.Recipient) error {
	if len(recipients) == 0 {
		return errors.New("there is no backup key to protect the key file with")
	}
	if keys.BackupIdentity != "" {
		return errors.New("the private backup key cannot be protected with itself; export it with a passphrase instead")
	}
	return writeKeys(w, keys, recipients...)
}

// ExportKeys writes keys to w, encrypted with a passphrase (age, scrypt). It is the way to
// protect the file when there is no backup key, or when the private backup key itself travels.
func ExportKeys(w io.Writer, keys Keys, passphrase string) error {
	passphrase = NormalizePassphrase(passphrase)
	if len(passphrase) < 16 {
		return errors.New("the passphrase is too short to protect the keys")
	}
	recipient, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return fmt.Errorf("cannot use the passphrase: %w", err)
	}
	recipient.SetWorkFactor(scryptWorkFactor)
	return writeKeys(w, keys, recipient)
}

func writeKeys(w io.Writer, keys Keys, recipients ...age.Recipient) error {
	if strings.TrimSpace(keys.EncryptionKey) == "" {
		return errors.New("there is no ENCRYPTION_KEY to export")
	}
	keys.Format = KeyFileFormat
	if keys.CreatedAt.IsZero() {
		keys.CreatedAt = time.Now().UTC().Truncate(time.Second)
	}
	body, err := json.Marshal(keys)
	if err != nil {
		return err
	}
	enc, err := age.Encrypt(w, recipients...)
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

// errKeyFileNotOpened is returned for a wrong secret and for a damaged file alike: without the
// right secret the file cannot be told apart from noise.
var errKeyFileNotOpened = errors.New("does not open this key file, or the file is damaged")

// ImportKeys reads a key file written by ExportKeys, with its passphrase.
func ImportKeys(r io.Reader, passphrase string) (Keys, error) {
	identity, err := age.NewScryptIdentity(NormalizePassphrase(passphrase))
	if err != nil {
		return Keys{}, fmt.Errorf("the passphrase %w", errKeyFileNotOpened)
	}
	keys, err := readKeys(r, identity)
	if errors.Is(err, errKeyFileNotOpened) {
		return Keys{}, fmt.Errorf("the passphrase %w", err)
	}
	return keys, err
}

// ImportKeysWith reads a key file written by ExportKeysFor, with the private backup key.
func ImportKeysWith(r io.Reader, identities []age.Identity) (Keys, error) {
	if len(identities) == 0 {
		return Keys{}, errors.New("the key file is protected with the backup key: set BACKUP_AGE_IDENTITY_FILE to the private backup key")
	}
	keys, err := readKeys(r, identities...)
	if errors.Is(err, errKeyFileNotOpened) {
		return Keys{}, fmt.Errorf("the backup key %w", err)
	}
	return keys, err
}

func readKeys(r io.Reader, identities ...age.Identity) (Keys, error) {
	plain, err := age.Decrypt(r, identities...)
	if err != nil {
		return Keys{}, errKeyFileNotOpened
	}
	body, err := io.ReadAll(io.LimitReader(plain, 1<<20))
	if err != nil {
		return Keys{}, errKeyFileNotOpened
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
