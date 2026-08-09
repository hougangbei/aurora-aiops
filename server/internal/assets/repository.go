package assets

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"modernc.org/sqlite"
)

const serverSelectColumns = `
	s.id, s.name, s.address, s.username, s.credential_id,
	s.host_key_fingerprint, s.ssh_port, s.status, s.status_message,
	s.os_family, s.os_version, s.architecture, s.cpu_cores,
	s.memory_bytes, s.disk_bytes, s.last_seen_at, s.last_collected_at,
	s.created_at, s.updated_at, c.auth_type`

const repositoryTimeFormat = "2006-01-02T15:04:05.000000000Z07:00"

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) CreateServer(ctx context.Context, server Server, credential StoredCredential) (Server, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Server{}, fmt.Errorf("create server begin transaction: %w", err)
	}
	defer tx.Rollback()

	credential = cloneStoredCredential(credential)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO asset_credentials (id, auth_type, nonce, ciphertext, key_version, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`, credential.ID, credential.AuthType, credential.Envelope.Nonce,
		credential.Envelope.Ciphertext, credential.Envelope.KeyVersion, formatRepositoryTime(credential.CreatedAt),
		formatRepositoryTime(credential.UpdatedAt)); err != nil {
		return Server{}, fmt.Errorf("create server insert credential: %w", err)
	}

	server.CredentialID = credential.ID
	if _, err := tx.ExecContext(ctx, `
INSERT INTO asset_servers (
	id, name, address, ssh_port, username, credential_id, host_key_fingerprint,
	status, status_message, os_family, os_version, architecture, cpu_cores,
	memory_bytes, disk_bytes, last_seen_at, last_collected_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		server.ID, server.Name, server.Address, server.SSHPort, server.Username, server.CredentialID,
		server.HostKeyFingerprint, server.Status, server.StatusMessage, server.OSFamily, server.OSVersion,
		server.Architecture, server.CPUCores, server.MemoryBytes, server.DiskBytes,
		formatRepositoryOptionalTime(server.LastSeenAt), formatRepositoryOptionalTime(server.LastCollectedAt),
		formatRepositoryTime(server.CreatedAt), formatRepositoryTime(server.UpdatedAt)); err != nil {
		if isServerNameConflict(ctx, tx, server.Name, err) {
			return Server{}, fmt.Errorf("create server: %w", ErrNameConflict)
		}
		return Server{}, fmt.Errorf("create server insert server: %w", err)
	}

	created, err := queryServer(ctx, tx, server.ID)
	if err != nil {
		return Server{}, fmt.Errorf("create server read result: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Server{}, fmt.Errorf("create server commit: %w", err)
	}
	return created, nil
}

func (r *Repository) UpdateServer(ctx context.Context, server Server, credential *StoredCredential) (Server, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Server{}, fmt.Errorf("update server begin transaction: %w", err)
	}
	defer tx.Rollback()

	var oldCredentialID string
	if err := tx.QueryRowContext(ctx, `SELECT credential_id FROM asset_servers WHERE id = ?`, server.ID).Scan(&oldCredentialID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Server{}, ErrNotFound
		}
		return Server{}, fmt.Errorf("update server read credential reference: %w", err)
	}

	credentialID := oldCredentialID
	if credential != nil {
		replacement := cloneStoredCredential(*credential)
		credentialID = replacement.ID
		if replacement.ID == oldCredentialID {
			result, err := tx.ExecContext(ctx, `
UPDATE asset_credentials
SET auth_type = ?, nonce = ?, ciphertext = ?, key_version = ?, created_at = ?, updated_at = ?
WHERE id = ?`, replacement.AuthType, replacement.Envelope.Nonce, replacement.Envelope.Ciphertext,
				replacement.Envelope.KeyVersion, formatRepositoryTime(replacement.CreatedAt),
				formatRepositoryTime(replacement.UpdatedAt), replacement.ID)
			if err != nil {
				return Server{}, fmt.Errorf("update server replace credential: %w", err)
			}
			if affected, err := result.RowsAffected(); err != nil {
				return Server{}, fmt.Errorf("update server count replaced credentials: %w", err)
			} else if affected == 0 {
				return Server{}, ErrNotFound
			}
		} else {
			if _, err := tx.ExecContext(ctx, `
INSERT INTO asset_credentials (id, auth_type, nonce, ciphertext, key_version, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`, replacement.ID, replacement.AuthType, replacement.Envelope.Nonce,
				replacement.Envelope.Ciphertext, replacement.Envelope.KeyVersion,
				formatRepositoryTime(replacement.CreatedAt), formatRepositoryTime(replacement.UpdatedAt)); err != nil {
				return Server{}, fmt.Errorf("update server insert replacement credential: %w", err)
			}
		}
	}

	result, err := tx.ExecContext(ctx, `
UPDATE asset_servers SET
	name = ?, address = ?, ssh_port = ?, username = ?, credential_id = ?,
	host_key_fingerprint = ?, status = ?, status_message = ?, os_family = ?,
	os_version = ?, architecture = ?, cpu_cores = ?, memory_bytes = ?, disk_bytes = ?,
	last_seen_at = ?, last_collected_at = ?, updated_at = ?
WHERE id = ?`, server.Name, server.Address, server.SSHPort, server.Username, credentialID,
		server.HostKeyFingerprint, server.Status, server.StatusMessage, server.OSFamily, server.OSVersion,
		server.Architecture, server.CPUCores, server.MemoryBytes, server.DiskBytes,
		formatRepositoryOptionalTime(server.LastSeenAt), formatRepositoryOptionalTime(server.LastCollectedAt),
		formatRepositoryTime(server.UpdatedAt), server.ID)
	if err != nil {
		if isServerNameConflict(ctx, tx, server.Name, err) {
			return Server{}, fmt.Errorf("update server: %w", ErrNameConflict)
		}
		return Server{}, fmt.Errorf("update server write server: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return Server{}, fmt.Errorf("update server count updated servers: %w", err)
	} else if affected == 0 {
		return Server{}, ErrNotFound
	}

	if credential != nil && credentialID != oldCredentialID {
		if _, err := tx.ExecContext(ctx, `
DELETE FROM asset_credentials
WHERE id = ? AND NOT EXISTS (SELECT 1 FROM asset_servers WHERE credential_id = ?)`, oldCredentialID, oldCredentialID); err != nil {
			return Server{}, fmt.Errorf("update server delete unreferenced credential: %w", err)
		}
	}

	updated, err := queryServer(ctx, tx, server.ID)
	if err != nil {
		return Server{}, fmt.Errorf("update server read result: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Server{}, fmt.Errorf("update server commit: %w", err)
	}
	return updated, nil
}

func (r *Repository) GetServer(ctx context.Context, id string) (Server, error) {
	server, err := queryServer(ctx, r.db, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Server{}, ErrNotFound
	}
	if err != nil {
		return Server{}, fmt.Errorf("get server: %w", err)
	}
	return server, nil
}

func (r *Repository) ListServers(ctx context.Context) ([]Server, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+serverSelectColumns+`
FROM asset_servers s
JOIN asset_credentials c ON c.id = s.credential_id
ORDER BY s.created_at, s.id`)
	if err != nil {
		return nil, fmt.Errorf("list servers: %w", err)
	}
	defer rows.Close()

	servers := make([]Server, 0)
	for rows.Next() {
		server, err := scanServer(rows)
		if err != nil {
			return nil, fmt.Errorf("list servers scan: %w", err)
		}
		servers = append(servers, server)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list servers rows: %w", err)
	}
	return servers, nil
}

func (r *Repository) DeleteServer(ctx context.Context, id string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete server begin transaction: %w", err)
	}
	defer tx.Rollback()

	var credentialID string
	if err := tx.QueryRowContext(ctx, `SELECT credential_id FROM asset_servers WHERE id = ?`, id).Scan(&credentialID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("delete server read credential reference: %w", err)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM asset_servers WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete server: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("delete server count deleted servers: %w", err)
	} else if affected == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `
DELETE FROM asset_credentials
WHERE id = ? AND NOT EXISTS (SELECT 1 FROM asset_servers WHERE credential_id = ?)`, credentialID, credentialID); err != nil {
		return fmt.Errorf("delete server delete unreferenced credential: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete server commit: %w", err)
	}
	return nil
}

func (r *Repository) GetCredential(ctx context.Context, id string) (StoredCredential, error) {
	var credential StoredCredential
	var createdAt, updatedAt string
	err := r.db.QueryRowContext(ctx, `
SELECT id, auth_type, nonce, ciphertext, key_version, created_at, updated_at
FROM asset_credentials WHERE id = ?`, id).Scan(&credential.ID, &credential.AuthType,
		&credential.Envelope.Nonce, &credential.Envelope.Ciphertext, &credential.Envelope.KeyVersion,
		&createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return StoredCredential{}, ErrNotFound
	}
	if err != nil {
		return StoredCredential{}, fmt.Errorf("get credential: %w", err)
	}
	credential.CreatedAt, err = parseRepositoryTime(createdAt)
	if err != nil {
		return StoredCredential{}, fmt.Errorf("get credential parse created time: %w", err)
	}
	credential.UpdatedAt, err = parseRepositoryTime(updatedAt)
	if err != nil {
		return StoredCredential{}, fmt.Errorf("get credential parse updated time: %w", err)
	}
	return cloneStoredCredential(credential), nil
}

func (r *Repository) ConfirmHostKey(ctx context.Context, id, fingerprint string, now time.Time) error {
	result, err := r.db.ExecContext(ctx, `
UPDATE asset_servers SET host_key_fingerprint = ?, updated_at = ? WHERE id = ?`,
		fingerprint, formatRepositoryTime(now), id)
	if err != nil {
		return fmt.Errorf("confirm server host key: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("confirm server host key count updated servers: %w", err)
	} else if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) SaveCollection(ctx context.Context, server Server, snapshot Snapshot, software []SoftwareItem) error {
	if server.ID != snapshot.ServerID {
		return fmt.Errorf("save collection server %q does not own snapshot for %q: %w", server.ID, snapshot.ServerID, ErrInvalidInput)
	}

	payload, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("save collection encode snapshot: %w", err)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("save collection begin transaction: %w", err)
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM asset_servers WHERE id = ?`, server.ID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("save collection find server: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO asset_snapshots (id, server_id, payload, collected_at)
VALUES (?, ?, ?, ?)`, snapshot.ID, snapshot.ServerID, string(payload), formatRepositoryTime(snapshot.CollectedAt)); err != nil {
		return fmt.Errorf("save collection insert snapshot: %w", err)
	}
	for _, item := range software {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO asset_software_items (snapshot_id, category, name, version, architecture, source, status)
VALUES (?, ?, ?, ?, ?, ?, ?)`, snapshot.ID, item.Category, item.Name, item.Version,
			item.Architecture, item.Source, item.Status); err != nil {
			return fmt.Errorf("save collection insert software item: %w", err)
		}
	}

	result, err := tx.ExecContext(ctx, `
UPDATE asset_servers SET
	os_family = ?, os_version = ?, architecture = ?, cpu_cores = ?, memory_bytes = ?, disk_bytes = ?,
	status = ?, status_message = '', last_seen_at = ?, last_collected_at = ?, updated_at = ?
WHERE id = ?`, snapshot.OSFamily, snapshot.OSVersion, snapshot.Architecture, snapshot.CPUCores,
		snapshot.MemoryBytes, snapshot.DiskBytes, ServerOnline, formatRepositoryTime(snapshot.CollectedAt),
		formatRepositoryTime(snapshot.CollectedAt), formatRepositoryTime(snapshot.CollectedAt), server.ID)
	if err != nil {
		return fmt.Errorf("save collection update server summary: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("save collection count updated servers: %w", err)
	} else if affected == 0 {
		return ErrNotFound
	}

	if _, err := tx.ExecContext(ctx, `
DELETE FROM asset_snapshots
WHERE server_id = ? AND id IN (
	SELECT id FROM asset_snapshots
	WHERE server_id = ?
	ORDER BY collected_at DESC, id DESC
	LIMIT -1 OFFSET 30
)`, server.ID, server.ID); err != nil {
		return fmt.Errorf("save collection enforce snapshot retention: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("save collection commit: %w", err)
	}
	return nil
}

func (r *Repository) MarkCollectionFailure(ctx context.Context, id string, status ServerStatus, message string, now time.Time) error {
	result, err := r.db.ExecContext(ctx, `
UPDATE asset_servers SET status = ?, status_message = ?, updated_at = ? WHERE id = ?`,
		status, message, formatRepositoryTime(now), id)
	if err != nil {
		return fmt.Errorf("mark collection failure: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("mark collection failure count updated servers: %w", err)
	} else if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) LatestSnapshot(ctx context.Context, serverID string) (Snapshot, error) {
	var payload []byte
	err := r.db.QueryRowContext(ctx, `
SELECT payload FROM asset_snapshots
WHERE server_id = ?
ORDER BY collected_at DESC, id DESC
LIMIT 1`, serverID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, ErrNotFound
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("get latest snapshot: %w", err)
	}
	var snapshot Snapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("get latest snapshot decode payload: %w", err)
	}
	return snapshot, nil
}

func (r *Repository) ListLatestSoftware(ctx context.Context, serverID string) ([]SoftwareItem, error) {
	var exists int
	if err := r.db.QueryRowContext(ctx, `SELECT 1 FROM asset_servers WHERE id = ?`, serverID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("list latest software find server: %w", err)
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT category, name, version, architecture, source, status
FROM asset_software_items
WHERE snapshot_id = (
	SELECT id FROM asset_snapshots
	WHERE server_id = ?
	ORDER BY collected_at DESC, id DESC
	LIMIT 1
)
ORDER BY category, name, architecture`, serverID)
	if err != nil {
		return nil, fmt.Errorf("list latest software: %w", err)
	}
	defer rows.Close()

	items := make([]SoftwareItem, 0)
	for rows.Next() {
		var item SoftwareItem
		if err := rows.Scan(&item.Category, &item.Name, &item.Version, &item.Architecture, &item.Source, &item.Status); err != nil {
			return nil, fmt.Errorf("list latest software scan: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list latest software rows: %w", err)
	}
	return items, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

type serverQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func queryServer(ctx context.Context, queryer serverQueryer, id string) (Server, error) {
	return scanServer(queryer.QueryRowContext(ctx, `SELECT `+serverSelectColumns+`
FROM asset_servers s
JOIN asset_credentials c ON c.id = s.credential_id
WHERE s.id = ?`, id))
}

func scanServer(row rowScanner) (Server, error) {
	var server Server
	var lastSeenAt, lastCollectedAt, createdAt, updatedAt string
	if err := row.Scan(&server.ID, &server.Name, &server.Address, &server.Username, &server.CredentialID,
		&server.HostKeyFingerprint, &server.SSHPort, &server.Status, &server.StatusMessage,
		&server.OSFamily, &server.OSVersion, &server.Architecture, &server.CPUCores,
		&server.MemoryBytes, &server.DiskBytes, &lastSeenAt, &lastCollectedAt,
		&createdAt, &updatedAt, &server.CredentialAuthType); err != nil {
		return Server{}, err
	}
	var err error
	server.LastSeenAt, err = parseRepositoryOptionalTime(lastSeenAt)
	if err != nil {
		return Server{}, fmt.Errorf("parse last seen time: %w", err)
	}
	server.LastCollectedAt, err = parseRepositoryOptionalTime(lastCollectedAt)
	if err != nil {
		return Server{}, fmt.Errorf("parse last collected time: %w", err)
	}
	server.CreatedAt, err = parseRepositoryTime(createdAt)
	if err != nil {
		return Server{}, fmt.Errorf("parse created time: %w", err)
	}
	server.UpdatedAt, err = parseRepositoryTime(updatedAt)
	if err != nil {
		return Server{}, fmt.Errorf("parse updated time: %w", err)
	}
	server.CredentialConfigured = true
	return server, nil
}

func isServerNameConflict(ctx context.Context, tx *sql.Tx, name string, err error) bool {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) || sqliteErr.Code() != 2067 {
		return false
	}
	var exists int
	return tx.QueryRowContext(ctx, `SELECT 1 FROM asset_servers WHERE name = ?`, name).Scan(&exists) == nil
}

func formatRepositoryTime(value time.Time) string {
	return value.UTC().Format(repositoryTimeFormat)
}

func formatRepositoryOptionalTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return formatRepositoryTime(*value)
}

func parseRepositoryTime(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
}

func parseRepositoryOptionalTime(value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := parseRepositoryTime(value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func cloneStoredCredential(credential StoredCredential) StoredCredential {
	credential.Envelope.Nonce = append([]byte(nil), credential.Envelope.Nonce...)
	credential.Envelope.Ciphertext = append([]byte(nil), credential.Envelope.Ciphertext...)
	return credential
}
