// SPDX-License-Identifier: AGPL-3.0-or-later
//
// RotatePanelKey — the v0.8.3 deferred work for
// re-provisioning v0.3.0..v0.7.x nodes.
//
// # Why this lives in its own file
//
// The v0.8.1 password-install path generated a
// fresh panel ed25519 keypair as a post-install
// hook (see buildPersistentSSHKeyHook in
// provisioner.go). v0.3.0..v0.7.x nodes — installed
// before the persistent key feature landed —
// have no stored panel key: the operator pasted
// their own PEM on every re-provision. v0.8.3
// ships the operator-side tool to back-fill the
// stored key on a legacy node.
//
// The CLI subcommand `aegis admin node
// rotate-panel-key <id> --key <path>` connects
// to the node over SSH using the operator's
// existing PEM, calls this function with the
// already-connected SSH client, and the function
// does the same gen + encrypt + push + persist
// flow the install hook does. After the call
// returns, the node row carries a non-empty
// `ssh_private_key_ciphertext`, and the next
// re-provision (via the UI, with no auth input
// from the operator) decrypts and reuses it —
// the "auto-deploy" experience v0.8.1 introduced
// becomes available retroactively on v0.3.0..v0.7.x
// nodes.
//
// # What this is NOT
//
//   - NOT a re-key. The new keypair REPLACES
//     nothing on the node (the operator's existing
//     key stays in authorized_keys; the new
//     public key is APPENDED). The next re-
//     provision will use the new key (the stored
//     one) because the provisioner's auth-method
//     precedence (v0.8.1) puts the stored key
//     over the request's auth. The operator's
//     key becomes a fallback (the operator can
//     still SSH in by hand if the panel-side
//     decryption breaks).
//
//   - NOT a re-install. The agent bearer is left
//     alone; the install workflow is not re-run.
//     `aegis admin node rotate-panel-key` is a
//     key-rotation tool, not a re-bootstrap.
//
//   - NOT a multi-tenant key. v0.8.1's stored
//     key is one-per-node. A future v0.8.x might
//     introduce a per-user or per-cluster key,
//     but the v0.8.3 work is one-per-node.

package bootstrap

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"

	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"

	"github.com/QAdversif/AegisPanel/internal/crypto/envelope"
)

// RotatePanelKey is the public entry point for
// the operator-side key rotation flow. The
// caller (the `aegis admin node rotate-panel-key`
// CLI subcommand) is expected to:
//
//  1. Resolve the node row to confirm the row
//     is in a state where rotation makes sense
//     (i.e. NOT pending first-time install).
//  2. Open an SSH connection to the node using
//     the operator's existing PEM (the
//     ClientConfig.PrivateKey field of
//     bootstrap.NewClient).
//  3. Call this function with the connected
//     client and the node UUID.
//
// The function does:
//
//  1. Generate a fresh ed25519 keypair on the
//     panel side.
//  2. Marshal the private key to OpenSSH PEM
//     and seal it with the operator's age
//     envelope.
//  3. Persist the ciphertext via
//     NodeProvider.SetSSHPrivateKeyCiphertext
//     so the next re-provision can decrypt and
//     reuse the same key.
//  4. Upload the public key to a fixed temp
//     path on the node via SFTP, then run a
//     constant shell command that appends the
//     key to $HOME/.ssh/authorized_keys.
//
// The function is fail-closed: any error in any
// step returns without leaving the panel in a
// half-rotated state (the SetSSHPrivateKeyCiphertext
// call is the only side effect that touches the
// DB; on failure the row's ciphertext column is
// unchanged, the operator can retry the CLI).
//
// The envelope must be non-nil. The caller is
// expected to have wired the production envelope
// (the same one `app/app.go` installs for the
// webhooks and bootstrap Services) before calling.
// A nil envelope returns an error without
// generating a keypair — fail-closed, since a
// "no envelope" path would persist plaintext
// PEM, which is the exact security regression the
// envelope is designed to prevent.
//
// The NodeProvider's SetSSHPrivateKeyCiphertext is
// the canonical "the key is on disk" signal; the
// caller (and any future code that reads the row)
// can verify by checking
// len(row.SSHPrivateKeyCiphertext) > 0. The
// provisioner's auth-method precedence
// (provisioner.go, v0.8.1) handles "stored key
// wins over request key" automatically.
//
// auditEntry / auditsSvc is optional; pass nil
// for the CLI path that does not have a request
// context (the CLI records the audit via the
// CLI-side audit helper, not the bootstrap
// Service).
func (s *Service) RotatePanelKey(
	ctx context.Context,
	nodeID uuid.UUID,
	nodeName string,
	sshClient Client,
) error {
	if s.envelope == nil {
		return fmt.Errorf("bootstrap: RotatePanelKey: envelope is not configured (set AEGIS_WEBHOOKS_SECRET_AGE_* env vars)")
	}
	if sshClient == nil {
		return fmt.Errorf("bootstrap: RotatePanelKey: sshClient is nil")
	}
	if err := s.generateAndPushKey(ctx, nodeID, nodeName, sshClient, s.envelope); err != nil {
		return fmt.Errorf("bootstrap: RotatePanelKey: %w", err)
	}
	return nil
}

// generateAndPushKey is the shared body of
// buildPersistentSSHKeyHook (the password-install
// post-install path) and RotatePanelKey (the
// operator-side CLI path). It does not touch the
// audit log — the caller records the audit (the
// provisioner has the per-call action name; the
// CLI records `node.rotate-panel-key`).
//
// nodeName is folded into the SSH key comment
// (`aegis-panel@node-<name>`) so the operator's
// `ssh-add -L` output is self-documenting in a
// multi-node fleet. It is also the comment the
// panel's own audit log will read back via the
// `adduser`-style line in the node's
// authorized_keys.
func (s *Service) generateAndPushKey(
	ctx context.Context,
	nodeID uuid.UUID,
	nodeName string,
	c Client,
	cipher envelope.SecretCipher,
) error {
	// 1. Generate ed25519 keypair.
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("ed25519.GenerateKey: %w", err)
	}
	// 2. Marshal private to OpenSSH PEM. The
	// comment is informational only (the SSH
	// client never sends it back to the
	// server); it shows up in `ssh-add -L`
	// and in the agent's debug logs.
	privPEMBlock, err := ssh.MarshalPrivateKey(priv, "aegis-panel@node-"+nodeName)
	if err != nil {
		return fmt.Errorf("ssh.MarshalPrivateKey: %w", err)
	}
	privPEM := pem.EncodeToMemory(privPEMBlock)
	// 3. Marshal public to authorized_keys
	// line. ssh.MarshalAuthorizedKey returns
	// "<key-type> <base64> <comment>\n" — the
	// trailing newline is trimmed so the
	// per-line `grep -qxF` idempotency check
	// on the remote side matches the line
	// shape we upload.
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return fmt.Errorf("ssh.NewPublicKey: %w", err)
	}
	pubLine := bytes.TrimSpace(ssh.MarshalAuthorizedKey(sshPub))
	// 4. Encrypt private key.
	encrypted, err := cipher.Encrypt(privPEM)
	if err != nil {
		return fmt.Errorf("envelope encrypt: %w", err)
	}
	// 5. Persist ciphertext. A failure here
	// leaves the operator's key in place on
	// the node (we have not yet pushed the
	// new public key) and the row's
	// ciphertext column is unchanged.
	if err := s.nodes.SetSSHPrivateKeyCiphertext(ctx, nodeID, encrypted); err != nil {
		return fmt.Errorf("persist ciphertext: %w", err)
	}
	// 6. Push public key. We upload the
	// single line to a fixed temp path on
	// the node via SFTP (the bytes are
	// written verbatim, so there is no
	// shell-quoting concern), then run a
	// CONSTANT shell command (no string
	// interpolation) that ensures
	// $HOME/.ssh exists with mode 0700,
	// creates authorized_keys with mode
	// 0600 if it does not exist,
	// idempotently appends the uploaded
	// line, and removes the temp file.
	// The `grep -qxF` check makes the
	// append idempotent on retry: a key
	// that is already in the file is
	// left untouched.
	//
	// The fixed path is safe because the
	// provisioner state machine forbids
	// concurrent provisions of the same
	// node (a node is `new` -> `online`
	// or `offline`; the state machine
	// rejects a re-provision from
	// `online`). On retry from `offline`
	// the previous temp file is left
	// behind but the `rm -f` at the end
	// of the new run cleans it up.
	//
	// The remote command is a constant
	// string — the public key is in a
	// file, not interpolated. gosec G204
	// (sub-shell injection) is satisfied
	// because the only `Run` argument
	// here is a compile-time string
	// literal.
	const remotePubKeyPath = "/tmp/.aegis-pubkey"
	// Upload the public key to a local
	// temp file first, then SFTP it onto
	// the node. The local file is the
	// panel-side source-of-truth for the
	// bytes; we clean it up below.
	localTmp, err := os.CreateTemp("", "aegis-pubkey-*.pub")
	if err != nil {
		return fmt.Errorf("local temp: %w", err)
	}
	localPath := localTmp.Name()
	defer func() { _ = os.Remove(localPath) }()
	if _, err := localTmp.Write(pubLine); err != nil {
		_ = localTmp.Close()
		return fmt.Errorf("local write: %w", err)
	}
	if err := localTmp.Close(); err != nil {
		return fmt.Errorf("local close: %w", err)
	}
	if err := c.Upload(ctx, localPath, remotePubKeyPath, 0o600); err != nil {
		return fmt.Errorf("sftp upload: %w", err)
	}
	// The remote command is a constant
	// string — the public key is in a
	// file, not interpolated. gosec G204
	// (sub-shell injection) is satisfied
	// because the only `Run` argument
	// here is a compile-time string
	// literal.
	const cmd = "set -e\n" +
		"install -d -m 0700 \"$HOME/.ssh\"\n" +
		"touch \"$HOME/.ssh/authorized_keys\"\n" +
		"chmod 0600 \"$HOME/.ssh/authorized_keys\"\n" +
		"PUBKEY_FILE=\"/tmp/.aegis-pubkey\"\n" +
		"if [ -f \"$PUBKEY_FILE\" ]; then\n" +
		"  if ! grep -qxF \"$(cat \"$PUBKEY_FILE\")\" \"$HOME/.ssh/authorized_keys\" 2>/dev/null; then\n" +
		"    cat \"$PUBKEY_FILE\" >> \"$HOME/.ssh/authorized_keys\"\n" +
		"  fi\n" +
		"  rm -f \"$PUBKEY_FILE\"\n" +
		"fi\n"
	if _, err := c.Run(ctx, cmd); err != nil {
		return fmt.Errorf("append authorized_keys: %w", err)
	}
	return nil
}
