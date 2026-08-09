package assets

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hougangbei/aurora-aiops/server/internal/store"
)

func TestRepositoryCreateIsAtomicAndPublicReadsHideCredentials(t *testing.T) {
	ctx := context.Background()
	db := openRepositoryDB(t)
	repo := NewRepository(db)
	now := time.Date(2026, 8, 9, 10, 0, 0, 123, time.UTC)
	credential := StoredCredential{
		ID:       "credential-1",
		AuthType: AuthPrivateKey,
		Envelope: CredentialEnvelope{
			Nonce:      []byte("nonce-canary"),
			Ciphertext: []byte("ciphertext-canary"),
			KeyVersion: 7,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	server := repositoryServer("server-1", "edge-1", "credential-1", now)

	created, err := repo.CreateServer(ctx, server, credential)
	if err != nil {
		t.Fatalf("CreateServer: %v", err)
	}
	if created.Status != ServerPending {
		t.Fatalf("status=%q want %q", created.Status, ServerPending)
	}
	if created.CredentialAuthType != AuthPrivateKey || !created.CredentialConfigured {
		t.Fatalf("credential metadata=%q configured=%v", created.CredentialAuthType, created.CredentialConfigured)
	}

	credential.Envelope.Nonce[0] = 'X'
	credential.Envelope.Ciphertext[0] = 'X'
	gotCredential, err := repo.GetCredential(ctx, "credential-1")
	if err != nil {
		t.Fatalf("GetCredential: %v", err)
	}
	if string(gotCredential.Envelope.Nonce) != "nonce-canary" || string(gotCredential.Envelope.Ciphertext) != "ciphertext-canary" || gotCredential.Envelope.KeyVersion != 7 {
		t.Fatalf("stored credential changed through input alias: %+v", gotCredential)
	}
	gotCredential.Envelope.Nonce[0] = 'Y'
	again, err := repo.GetCredential(ctx, "credential-1")
	if err != nil {
		t.Fatalf("GetCredential again: %v", err)
	}
	if string(again.Envelope.Nonce) != "nonce-canary" {
		t.Fatalf("stored credential changed through output alias: %q", again.Envelope.Nonce)
	}

	got, err := repo.GetServer(ctx, "server-1")
	if err != nil {
		t.Fatalf("GetServer: %v", err)
	}
	listed, err := repo.ListServers(ctx)
	if err != nil {
		t.Fatalf("ListServers: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("ListServers len=%d want 1", len(listed))
	}
	for name, value := range map[string]any{"get": got, "list": listed} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal %s: %v", name, err)
		}
		text := string(data)
		if strings.Contains(text, "nonce-canary") || strings.Contains(text, "ciphertext-canary") ||
			strings.Contains(text, base64.StdEncoding.EncodeToString([]byte("nonce-canary"))) ||
			strings.Contains(text, base64.StdEncoding.EncodeToString([]byte("ciphertext-canary"))) ||
			strings.Contains(text, "Envelope") {
			t.Fatalf("%s public JSON leaked credential: %s", name, text)
		}
	}
	if got.CredentialAuthType != AuthPrivateKey || !got.CredentialConfigured {
		t.Fatalf("GetServer credential metadata=%q configured=%v", got.CredentialAuthType, got.CredentialConfigured)
	}

	badCredential := StoredCredential{ID: "rolled-back", AuthType: AuthPassword, Envelope: CredentialEnvelope{Nonce: []byte{1}, Ciphertext: []byte{2}, KeyVersion: 1}, CreatedAt: now, UpdatedAt: now}
	badServer := repositoryServer("bad-server", "bad-server", badCredential.ID, now)
	badServer.SSHPort = 0
	if _, err := repo.CreateServer(ctx, badServer, badCredential); err == nil {
		t.Fatal("CreateServer with invalid port succeeded")
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM asset_credentials WHERE id = ?`, badCredential.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("credential count=%d want 0 after atomic create failure", count)
	}
}

func TestRepositoryNameConflictUpdateCredentialAndOrdering(t *testing.T) {
	ctx := context.Background()
	db := openRepositoryDB(t)
	repo := NewRepository(db)
	base := time.Date(2026, 8, 9, 11, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		id      string
		name    string
		created time.Time
	}{
		{id: "server-b", name: "bravo", created: base},
		{id: "server-a", name: "alpha", created: base},
		{id: "server-c", name: "charlie", created: base.Add(time.Second)},
	} {
		credential := repositoryCredential("credential-"+tc.id, AuthPassword, tc.created)
		if _, err := repo.CreateServer(ctx, repositoryServer(tc.id, tc.name, credential.ID, tc.created), credential); err != nil {
			t.Fatalf("CreateServer %s: %v", tc.id, err)
		}
	}

	duplicate := repositoryServer("server-duplicate", "alpha", "credential-duplicate", base.Add(2*time.Second))
	_, err := repo.CreateServer(ctx, duplicate, repositoryCredential(duplicate.CredentialID, AuthPassword, duplicate.CreatedAt))
	if !errors.Is(err, ErrNameConflict) {
		t.Fatalf("duplicate name error=%v want ErrNameConflict", err)
	}
	if _, err := repo.GetCredential(ctx, duplicate.CredentialID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("duplicate credential was not rolled back: %v", err)
	}

	servers, err := repo.ListServers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gotOrder := []string{servers[0].ID, servers[1].ID, servers[2].ID}
	wantOrder := []string{"server-a", "server-b", "server-c"}
	if !reflect.DeepEqual(gotOrder, wantOrder) {
		t.Fatalf("server order=%v want %v", gotOrder, wantOrder)
	}

	current, err := repo.GetServer(ctx, "server-a")
	if err != nil {
		t.Fatal(err)
	}
	originalCredentialID := current.CredentialID
	current.Address = "198.51.100.10"
	current.UpdatedAt = base.Add(3 * time.Second)
	updated, err := repo.UpdateServer(ctx, current, nil)
	if err != nil {
		t.Fatalf("UpdateServer without credential: %v", err)
	}
	if updated.CredentialID != originalCredentialID || updated.CredentialAuthType != AuthPassword {
		t.Fatalf("nil credential changed metadata: %+v", updated)
	}
	if _, err := repo.GetCredential(ctx, originalCredentialID); err != nil {
		t.Fatalf("preserved credential missing: %v", err)
	}

	replacement := repositoryCredential("credential-replacement", AuthPrivateKey, base.Add(4*time.Second))
	replacement.Envelope = CredentialEnvelope{Nonce: []byte("new-nonce"), Ciphertext: []byte("new-ciphertext"), KeyVersion: 9}
	current = updated
	current.Username = "deploy"
	current.UpdatedAt = replacement.UpdatedAt
	updated, err = repo.UpdateServer(ctx, current, &replacement)
	if err != nil {
		t.Fatalf("UpdateServer with credential: %v", err)
	}
	if updated.CredentialID != replacement.ID || updated.CredentialAuthType != AuthPrivateKey || !updated.CredentialConfigured {
		t.Fatalf("replacement metadata not reflected: %+v", updated)
	}
	storedReplacement, err := repo.GetCredential(ctx, replacement.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(storedReplacement.Envelope, replacement.Envelope) || storedReplacement.AuthType != replacement.AuthType {
		t.Fatalf("replacement=%+v want %+v", storedReplacement, replacement)
	}

	conflicting, err := repo.GetServer(ctx, "server-c")
	if err != nil {
		t.Fatal(err)
	}
	conflicting.Name = "alpha"
	conflicting.UpdatedAt = base.Add(5 * time.Second)
	rolledBackReplacement := repositoryCredential("credential-rolled-back", AuthPrivateKey, conflicting.UpdatedAt)
	if _, err := repo.UpdateServer(ctx, conflicting, &rolledBackReplacement); !errors.Is(err, ErrNameConflict) {
		t.Fatalf("conflicting replacement update error=%v want ErrNameConflict", err)
	}
	if _, err := repo.GetCredential(ctx, rolledBackReplacement.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("replacement credential survived failed update: %v", err)
	}
	unchanged, err := repo.GetServer(ctx, conflicting.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Name != "charlie" || unchanged.CredentialID != "credential-server-c" || unchanged.CredentialAuthType != AuthPassword {
		t.Fatalf("failed update partially committed: %+v", unchanged)
	}
}

func TestRepositorySaveCollectionIsAtomicAndListsSoftwareDeterministically(t *testing.T) {
	ctx := context.Background()
	db := openRepositoryDB(t)
	repo := NewRepository(db)
	createdAt := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	server := repositoryServer("server-1", "edge-1", "credential-1", createdAt)
	if _, err := repo.CreateServer(ctx, server, repositoryCredential(server.CredentialID, AuthPassword, createdAt)); err != nil {
		t.Fatal(err)
	}
	collectedAt := createdAt.Add(time.Minute)
	snapshot := Snapshot{ID: "snapshot-1", ServerID: server.ID, OSFamily: "linux", OSVersion: "24.04", KernelVersion: "6.8", Architecture: "amd64", Hostname: "edge-1", CPUCores: 8, MemoryBytes: 16 << 30, DiskBytes: 200 << 30, Load1: 0.25, UptimeSeconds: 3600, CollectedAt: collectedAt}
	software := []SoftwareItem{
		{Category: "system", Name: "zlib", Version: "1", Architecture: "amd64", Source: "apt", Status: "installed"},
		{Category: "runtime", Name: "go", Version: "1.25", Architecture: "amd64", Source: "path", Status: "available"},
		{Category: "runtime", Name: "go", Version: "1.24", Architecture: "arm64", Source: "path", Status: "available"},
	}
	if err := repo.SaveCollection(ctx, server, snapshot, software); err != nil {
		t.Fatalf("SaveCollection: %v", err)
	}

	gotServer, err := repo.GetServer(ctx, server.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotServer.Status != ServerOnline || gotServer.StatusMessage != "" || gotServer.OSFamily != snapshot.OSFamily || gotServer.OSVersion != snapshot.OSVersion || gotServer.Architecture != snapshot.Architecture || gotServer.CPUCores != snapshot.CPUCores || gotServer.MemoryBytes != snapshot.MemoryBytes || gotServer.DiskBytes != snapshot.DiskBytes {
		t.Fatalf("server summary not updated: %+v", gotServer)
	}
	if gotServer.LastSeenAt == nil || !gotServer.LastSeenAt.Equal(collectedAt) || gotServer.LastCollectedAt == nil || !gotServer.LastCollectedAt.Equal(collectedAt) {
		t.Fatalf("collection timestamps=%v/%v want %v", gotServer.LastSeenAt, gotServer.LastCollectedAt, collectedAt)
	}
	latest, err := repo.LatestSnapshot(ctx, server.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(latest, snapshot) {
		t.Fatalf("snapshot=%+v want %+v", latest, snapshot)
	}
	gotSoftware, err := repo.ListLatestSoftware(ctx, server.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantSoftware := []SoftwareItem{software[1], software[2], software[0]}
	if !reflect.DeepEqual(gotSoftware, wantSoftware) {
		t.Fatalf("software=%+v want %+v", gotSoftware, wantSoftware)
	}

	badSnapshot := snapshot
	badSnapshot.ID = "snapshot-bad"
	badSnapshot.OSVersion = "broken"
	badSnapshot.CollectedAt = collectedAt.Add(time.Minute)
	duplicateSoftware := []SoftwareItem{{Category: "system", Name: "curl", Architecture: "amd64"}, {Category: "system", Name: "curl", Architecture: "amd64"}}
	if err := repo.SaveCollection(ctx, gotServer, badSnapshot, duplicateSoftware); err == nil {
		t.Fatal("SaveCollection with duplicate software succeeded")
	}
	var badCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM asset_snapshots WHERE id = ?`, badSnapshot.ID).Scan(&badCount); err != nil {
		t.Fatal(err)
	}
	if badCount != 0 {
		t.Fatalf("partial snapshot count=%d want 0", badCount)
	}
	afterFailure, err := repo.GetServer(ctx, server.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterFailure.OSVersion != snapshot.OSVersion || afterFailure.LastCollectedAt == nil || !afterFailure.LastCollectedAt.Equal(collectedAt) {
		t.Fatalf("summary changed after rolled-back collection: %+v", afterFailure)
	}
}

func TestRepositorySaveCollectionRejectsMismatchedServerOwnership(t *testing.T) {
	ctx := context.Background()
	db := openRepositoryDB(t)
	repo := NewRepository(db)
	base := time.Date(2026, 8, 9, 12, 30, 0, 0, time.UTC)
	first := repositoryServer("server-1", "edge-1", "credential-1", base)
	second := repositoryServer("server-2", "edge-2", "credential-2", base.Add(time.Second))
	for _, server := range []Server{first, second} {
		if _, err := repo.CreateServer(ctx, server, repositoryCredential(server.CredentialID, AuthPassword, server.CreatedAt)); err != nil {
			t.Fatalf("CreateServer %s: %v", server.ID, err)
		}
	}
	firstBefore, err := repo.GetServer(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondBefore, err := repo.GetServer(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}

	snapshot := Snapshot{
		ID:           "cross-owned-snapshot",
		ServerID:     second.ID,
		OSFamily:     "linux",
		OSVersion:    "24.04",
		Architecture: "amd64",
		CPUCores:     16,
		CollectedAt:  base.Add(time.Minute),
	}
	software := []SoftwareItem{{Category: "system", Name: "must-not-persist", Architecture: "amd64"}}
	if err := repo.SaveCollection(ctx, first, snapshot, software); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("SaveCollection mismatched ownership error=%v want ErrInvalidInput", err)
	}

	var snapshotCount, softwareCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM asset_snapshots WHERE id = ?`, snapshot.ID).Scan(&snapshotCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM asset_software_items WHERE name = ?`, software[0].Name).Scan(&softwareCount); err != nil {
		t.Fatal(err)
	}
	if snapshotCount != 0 || softwareCount != 0 {
		t.Fatalf("mismatched collection persisted snapshot=%d software=%d", snapshotCount, softwareCount)
	}
	firstAfter, err := repo.GetServer(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondAfter, err := repo.GetServer(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(firstAfter, firstBefore) || !reflect.DeepEqual(secondAfter, secondBefore) {
		t.Fatalf("mismatched collection updated a server:\nfirst got %+v want %+v\nsecond got %+v want %+v", firstAfter, firstBefore, secondAfter, secondBefore)
	}
}

func TestRepositoryStaleCollectionDoesNotReplaceLatestInventoryOrServerState(t *testing.T) {
	ctx := context.Background()
	db := openRepositoryDB(t)
	repo := NewRepository(db)
	base := time.Date(2026, 8, 9, 12, 45, 0, 0, time.UTC)
	server := repositoryServer("server-1", "edge-1", "credential-1", base)
	if _, err := repo.CreateServer(ctx, server, repositoryCredential(server.CredentialID, AuthPassword, base)); err != nil {
		t.Fatal(err)
	}

	newer := Snapshot{
		ID:           "snapshot-newer",
		ServerID:     server.ID,
		OSFamily:     "new-linux",
		OSVersion:    "new-version",
		Architecture: "new-arch",
		CPUCores:     32,
		MemoryBytes:  64 << 30,
		DiskBytes:    500 << 30,
		CollectedAt:  base.Add(2 * time.Minute),
	}
	newerSoftware := []SoftwareItem{{Category: "runtime", Name: "newer-package", Architecture: "amd64"}}
	if err := repo.SaveCollection(ctx, server, newer, newerSoftware); err != nil {
		t.Fatal(err)
	}
	failureAt := base.Add(3 * time.Minute)
	if err := repo.MarkCollectionFailure(ctx, server.ID, ServerOffline, "failure after newest success", failureAt); err != nil {
		t.Fatal(err)
	}
	wantServer, err := repo.GetServer(ctx, server.ID)
	if err != nil {
		t.Fatal(err)
	}

	older := Snapshot{
		ID:           "snapshot-older",
		ServerID:     server.ID,
		OSFamily:     "old-linux",
		OSVersion:    "old-version",
		Architecture: "old-arch",
		CPUCores:     1,
		MemoryBytes:  1 << 30,
		DiskBytes:    10 << 30,
		CollectedAt:  base.Add(time.Minute),
	}
	if err := repo.SaveCollection(ctx, server, older, []SoftwareItem{{Category: "runtime", Name: "older-package", Architecture: "arm64"}}); err != nil {
		t.Fatal(err)
	}

	latest, err := repo.LatestSnapshot(ctx, server.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(latest, newer) {
		t.Fatalf("latest snapshot=%+v want newer %+v", latest, newer)
	}
	items, err := repo.ListLatestSoftware(ctx, server.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(items, newerSoftware) {
		t.Fatalf("latest software=%+v want %+v", items, newerSoftware)
	}
	gotServer, err := repo.GetServer(ctx, server.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotServer, wantServer) {
		t.Fatalf("stale collection replaced server state:\n got %+v\nwant %+v", gotServer, wantServer)
	}
	var snapshotCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM asset_snapshots WHERE server_id = ?`, server.ID).Scan(&snapshotCount); err != nil {
		t.Fatal(err)
	}
	if snapshotCount != 2 {
		t.Fatalf("snapshot count=%d want retained newer and older rows", snapshotCount)
	}
}

func TestRepositoryRetainsNewestThirtySuccessfulSnapshots(t *testing.T) {
	ctx := context.Background()
	db := openRepositoryDB(t)
	repo := NewRepository(db)
	base := time.Date(2026, 8, 9, 13, 0, 0, 0, time.UTC)
	server := repositoryServer("server-1", "edge-1", "credential-1", base)
	if _, err := repo.CreateServer(ctx, server, repositoryCredential(server.CredentialID, AuthPassword, base)); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 31; i++ {
		snapshot := Snapshot{ID: fmt.Sprintf("snapshot-%02d", i), ServerID: server.ID, OSFamily: "linux", Architecture: "amd64", CollectedAt: base}
		items := []SoftwareItem{{Category: "system", Name: fmt.Sprintf("package-%02d", i), Architecture: "amd64"}}
		if err := repo.SaveCollection(ctx, server, snapshot, items); err != nil {
			t.Fatalf("SaveCollection %d: %v", i, err)
		}
	}

	var snapshotCount, firstSnapshotCount, firstSoftwareCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM asset_snapshots WHERE server_id = ?`, server.ID).Scan(&snapshotCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM asset_snapshots WHERE id = 'snapshot-01'`).Scan(&firstSnapshotCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM asset_software_items WHERE name = 'package-01'`).Scan(&firstSoftwareCount); err != nil {
		t.Fatal(err)
	}
	if snapshotCount != 30 || firstSnapshotCount != 0 || firstSoftwareCount != 0 {
		t.Fatalf("retention counts snapshots=%d first=%d first software=%d", snapshotCount, firstSnapshotCount, firstSoftwareCount)
	}
	latest, err := repo.LatestSnapshot(ctx, server.ID)
	if err != nil {
		t.Fatal(err)
	}
	if latest.ID != "snapshot-31" {
		t.Fatalf("latest snapshot=%q want snapshot-31", latest.ID)
	}
}

func TestRepositoryLatestSnapshotUsesIDDescendingForTimestampTie(t *testing.T) {
	for _, tc := range []struct {
		name  string
		order []string
	}{
		{name: "higher ID inserted last", order: []string{"snapshot-a", "snapshot-z"}},
		{name: "higher ID inserted first", order: []string{"snapshot-z", "snapshot-a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db := openRepositoryDB(t)
			repo := NewRepository(db)
			base := time.Date(2026, 8, 9, 13, 15, 0, 0, time.UTC)
			server := repositoryServer("server-1", "edge-1", "credential-1", base)
			if _, err := repo.CreateServer(ctx, server, repositoryCredential(server.CredentialID, AuthPassword, base)); err != nil {
				t.Fatal(err)
			}
			snapshots := map[string]Snapshot{
				"snapshot-a": {ID: "snapshot-a", ServerID: server.ID, OSFamily: "from-a", CPUCores: 1, CollectedAt: base.Add(time.Minute)},
				"snapshot-z": {ID: "snapshot-z", ServerID: server.ID, OSFamily: "from-z", CPUCores: 64, CollectedAt: base.Add(time.Minute)},
			}
			for _, id := range tc.order {
				snapshot := snapshots[id]
				if err := repo.SaveCollection(ctx, server, snapshot, []SoftwareItem{{Category: "runtime", Name: "software-" + id}}); err != nil {
					t.Fatalf("SaveCollection %s: %v", id, err)
				}
			}

			latest, err := repo.LatestSnapshot(ctx, server.ID)
			if err != nil {
				t.Fatal(err)
			}
			if latest.ID != "snapshot-z" {
				t.Fatalf("latest snapshot=%q want snapshot-z", latest.ID)
			}
			items, err := repo.ListLatestSoftware(ctx, server.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 1 || items[0].Name != "software-snapshot-z" {
				t.Fatalf("latest software=%+v want snapshot-z software", items)
			}
			gotServer, err := repo.GetServer(ctx, server.ID)
			if err != nil {
				t.Fatal(err)
			}
			if gotServer.OSFamily != "from-z" || gotServer.CPUCores != 64 {
				t.Fatalf("server summary=%+v want snapshot-z summary", gotServer)
			}
		})
	}
}

func TestRepositoryLatestUsesChronologicalTimeBeforeIDTieBreak(t *testing.T) {
	ctx := context.Background()
	db := openRepositoryDB(t)
	repo := NewRepository(db)
	base := time.Date(2026, 8, 9, 13, 30, 0, 0, time.UTC)
	server := repositoryServer("server-1", "edge-1", "credential-1", base)
	if _, err := repo.CreateServer(ctx, server, repositoryCredential(server.CredentialID, AuthPassword, base)); err != nil {
		t.Fatal(err)
	}

	later := Snapshot{ID: "snapshot-a", ServerID: server.ID, CollectedAt: base.Add(100 * time.Millisecond)}
	if err := repo.SaveCollection(ctx, server, later, []SoftwareItem{{Category: "runtime", Name: "later"}}); err != nil {
		t.Fatal(err)
	}
	earlier := Snapshot{ID: "snapshot-z", ServerID: server.ID, CollectedAt: base}
	if err := repo.SaveCollection(ctx, server, earlier, []SoftwareItem{{Category: "runtime", Name: "earlier"}}); err != nil {
		t.Fatal(err)
	}

	latest, err := repo.LatestSnapshot(ctx, server.ID)
	if err != nil {
		t.Fatal(err)
	}
	if latest.ID != later.ID {
		t.Fatalf("latest snapshot=%q want chronologically later %q", latest.ID, later.ID)
	}
	items, err := repo.ListLatestSoftware(ctx, server.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Name != "later" {
		t.Fatalf("latest software=%+v want later snapshot software", items)
	}
}

func TestRepositoryFailureAndHostKeyOnlyChangeIntendedServerFields(t *testing.T) {
	ctx := context.Background()
	db := openRepositoryDB(t)
	repo := NewRepository(db)
	base := time.Date(2026, 8, 9, 14, 0, 0, 0, time.UTC)
	server := repositoryServer("server-1", "edge-1", "credential-1", base)
	if _, err := repo.CreateServer(ctx, server, repositoryCredential(server.CredentialID, AuthPassword, base)); err != nil {
		t.Fatal(err)
	}
	snapshot := Snapshot{ID: "snapshot-1", ServerID: server.ID, OSFamily: "linux", OSVersion: "24.04", Architecture: "amd64", CollectedAt: base.Add(time.Minute)}
	software := []SoftwareItem{{Category: "system", Name: "curl", Architecture: "amd64"}}
	if err := repo.SaveCollection(ctx, server, snapshot, software); err != nil {
		t.Fatal(err)
	}

	failureAt := base.Add(2 * time.Minute)
	if err := repo.MarkCollectionFailure(ctx, server.ID, ServerError, "ssh timeout", failureAt); err != nil {
		t.Fatal(err)
	}
	afterFailure, err := repo.GetServer(ctx, server.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterFailure.Status != ServerError || afterFailure.StatusMessage != "ssh timeout" || !afterFailure.UpdatedAt.Equal(failureAt) {
		t.Fatalf("failure fields not updated: %+v", afterFailure)
	}
	if afterFailure.LastCollectedAt == nil || !afterFailure.LastCollectedAt.Equal(snapshot.CollectedAt) || afterFailure.OSVersion != snapshot.OSVersion {
		t.Fatalf("successful summary replaced by failure: %+v", afterFailure)
	}
	latest, err := repo.LatestSnapshot(ctx, server.ID)
	if err != nil || latest.ID != snapshot.ID {
		t.Fatalf("latest after failure=%+v err=%v", latest, err)
	}
	items, err := repo.ListLatestSoftware(ctx, server.ID)
	if err != nil || !reflect.DeepEqual(items, software) {
		t.Fatalf("software after failure=%+v err=%v", items, err)
	}

	beforeHostKey := afterFailure
	confirmedAt := base.Add(3 * time.Minute)
	if err := repo.ConfirmHostKey(ctx, server.ID, "SHA256:fingerprint", confirmedAt); err != nil {
		t.Fatal(err)
	}
	afterHostKey, err := repo.GetServer(ctx, server.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterHostKey.HostKeyFingerprint != "SHA256:fingerprint" || !afterHostKey.UpdatedAt.Equal(confirmedAt) {
		t.Fatalf("host key fields not updated: %+v", afterHostKey)
	}
	afterHostKey.HostKeyFingerprint = beforeHostKey.HostKeyFingerprint
	afterHostKey.UpdatedAt = beforeHostKey.UpdatedAt
	if !reflect.DeepEqual(afterHostKey, beforeHostKey) {
		t.Fatalf("ConfirmHostKey changed unrelated fields:\n got %+v\nwant %+v", afterHostKey, beforeHostKey)
	}
}

func TestRepositoryDeleteCascadesAndNotFoundAndEmptyListsAreStable(t *testing.T) {
	ctx := context.Background()
	db := openRepositoryDB(t)
	repo := NewRepository(db)
	empty, err := repo.ListServers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty server list=%#v want non-nil empty", empty)
	}

	base := time.Date(2026, 8, 9, 15, 0, 0, 0, time.UTC)
	server := repositoryServer("server-1", "edge-1", "credential-1", base)
	if _, err := repo.CreateServer(ctx, server, repositoryCredential(server.CredentialID, AuthPassword, base)); err != nil {
		t.Fatal(err)
	}
	noSoftware, err := repo.ListLatestSoftware(ctx, server.ID)
	if err != nil {
		t.Fatal(err)
	}
	if noSoftware == nil || len(noSoftware) != 0 {
		t.Fatalf("empty software list=%#v want non-nil empty", noSoftware)
	}
	snapshot := Snapshot{ID: "snapshot-1", ServerID: server.ID, CollectedAt: base.Add(time.Minute)}
	if err := repo.SaveCollection(ctx, server, snapshot, []SoftwareItem{{Category: "system", Name: "curl"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO project_installations (id, server_id, project_id, version, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, "installation-1", server.ID, "project-1", "1.0", "installed", base.Format(time.RFC3339Nano), base.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteServer(ctx, server.ID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"asset_servers", "asset_credentials", "asset_snapshots", "asset_software_items", "project_installations"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s count=%d want 0", table, count)
		}
	}

	missingServer := repositoryServer("missing", "missing", "missing-credential", base)
	missingSnapshot := Snapshot{ID: "missing-snapshot", ServerID: missingServer.ID, CollectedAt: base}
	checks := map[string]error{}
	_, checks["GetServer"] = repo.GetServer(ctx, missingServer.ID)
	_, checks["UpdateServer"] = repo.UpdateServer(ctx, missingServer, nil)
	checks["DeleteServer"] = repo.DeleteServer(ctx, missingServer.ID)
	_, checks["GetCredential"] = repo.GetCredential(ctx, missingServer.CredentialID)
	checks["ConfirmHostKey"] = repo.ConfirmHostKey(ctx, missingServer.ID, "fingerprint", base)
	checks["SaveCollection"] = repo.SaveCollection(ctx, missingServer, missingSnapshot, nil)
	checks["MarkCollectionFailure"] = repo.MarkCollectionFailure(ctx, missingServer.ID, ServerOffline, "missing", base)
	_, checks["LatestSnapshot"] = repo.LatestSnapshot(ctx, missingServer.ID)
	_, checks["ListLatestSoftware"] = repo.ListLatestSoftware(ctx, missingServer.ID)
	for operation, err := range checks {
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("%s error=%v want ErrNotFound", operation, err)
		}
	}
}

func TestRepositoryDeletePreservesSharedCredentialUntilFinalReference(t *testing.T) {
	ctx := context.Background()
	db := openRepositoryDB(t)
	repo := NewRepository(db)
	base := time.Date(2026, 8, 9, 16, 0, 0, 0, time.UTC)
	first := repositoryServer("server-1", "edge-1", "shared-credential", base)
	if _, err := repo.CreateServer(ctx, first, repositoryCredential(first.CredentialID, AuthPassword, base)); err != nil {
		t.Fatal(err)
	}
	second := repositoryServer("server-2", "edge-2", first.CredentialID, base.Add(time.Second))
	if _, err := db.Exec(`
INSERT INTO asset_servers (id, name, address, ssh_port, username, credential_id, status, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, second.ID, second.Name, second.Address, second.SSHPort,
		second.Username, second.CredentialID, second.Status, formatRepositoryTime(second.CreatedAt),
		formatRepositoryTime(second.UpdatedAt)); err != nil {
		t.Fatalf("seed second shared-credential server: %v", err)
	}
	snapshot := Snapshot{ID: "snapshot-1", ServerID: first.ID, CollectedAt: base.Add(time.Minute)}
	if err := repo.SaveCollection(ctx, first, snapshot, []SoftwareItem{{Category: "system", Name: "curl"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
INSERT INTO project_installations (id, server_id, project_id, version, status, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`, "installation-1", first.ID, "project-1", "1.0", "installed",
		formatRepositoryTime(base), formatRepositoryTime(base)); err != nil {
		t.Fatal(err)
	}

	if err := repo.DeleteServer(ctx, first.ID); err != nil {
		t.Fatalf("DeleteServer first: %v", err)
	}
	for table, query := range map[string]string{
		"first server":       `SELECT COUNT(*) FROM asset_servers WHERE id = 'server-1'`,
		"first snapshots":    `SELECT COUNT(*) FROM asset_snapshots WHERE server_id = 'server-1'`,
		"first software":     `SELECT COUNT(*) FROM asset_software_items`,
		"first installation": `SELECT COUNT(*) FROM project_installations WHERE server_id = 'server-1'`,
	} {
		var count int
		if err := db.QueryRow(query).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s count=%d want 0", table, count)
		}
	}
	if _, err := repo.GetServer(ctx, second.ID); err != nil {
		t.Fatalf("second server removed with first: %v", err)
	}
	if _, err := repo.GetCredential(ctx, first.CredentialID); err != nil {
		t.Fatalf("shared credential removed with first reference: %v", err)
	}

	if err := repo.DeleteServer(ctx, second.ID); err != nil {
		t.Fatalf("DeleteServer final reference: %v", err)
	}
	if _, err := repo.GetCredential(ctx, first.CredentialID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("shared credential after final delete error=%v want ErrNotFound", err)
	}
}

func TestRepositoryUpdateSharedCredentialRequiresCopyOnWrite(t *testing.T) {
	ctx := context.Background()
	db := openRepositoryDB(t)
	repo := NewRepository(db)
	base := time.Date(2026, 8, 9, 16, 30, 0, 0, time.UTC)
	first := repositoryServer("server-1", "edge-1", "credential-c1", base)
	if _, err := repo.CreateServer(ctx, first, repositoryCredential(first.CredentialID, AuthPassword, base)); err != nil {
		t.Fatal(err)
	}
	second := repositoryServer("server-2", "edge-2", first.CredentialID, base.Add(time.Second))
	if _, err := db.Exec(`
INSERT INTO asset_servers (id, name, address, ssh_port, username, credential_id, status, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, second.ID, second.Name, second.Address, second.SSHPort,
		second.Username, second.CredentialID, second.Status, formatRepositoryTime(second.CreatedAt),
		formatRepositoryTime(second.UpdatedAt)); err != nil {
		t.Fatalf("seed shared-credential server: %v", err)
	}
	firstBefore, err := repo.GetServer(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondBefore, err := repo.GetServer(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	c1Before, err := repo.GetCredential(ctx, first.CredentialID)
	if err != nil {
		t.Fatal(err)
	}

	sharedSameID := repositoryCredential(first.CredentialID, AuthPrivateKey, base.Add(time.Minute))
	sharedSameID.Envelope = CredentialEnvelope{Nonce: []byte("forbidden-nonce"), Ciphertext: []byte("forbidden-ciphertext"), KeyVersion: 99}
	firstBefore.Username = "must-not-update"
	if _, err := repo.UpdateServer(ctx, firstBefore, &sharedSameID); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("shared same-ID update error=%v want ErrInvalidInput", err)
	}
	firstAfterRejected, err := repo.GetServer(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondAfterRejected, err := repo.GetServer(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	c1AfterRejected, err := repo.GetCredential(ctx, first.CredentialID)
	if err != nil {
		t.Fatal(err)
	}
	firstBefore.Username = "root"
	if !reflect.DeepEqual(firstAfterRejected, firstBefore) || !reflect.DeepEqual(secondAfterRejected, secondBefore) || !reflect.DeepEqual(c1AfterRejected, c1Before) {
		t.Fatalf("rejected shared credential update changed state:\nfirst %+v\nsecond %+v\ncredential %+v", firstAfterRejected, secondAfterRejected, c1AfterRejected)
	}

	c2 := repositoryCredential("credential-c2", AuthPrivateKey, base.Add(2*time.Minute))
	firstForCopy := firstAfterRejected
	firstForCopy.UpdatedAt = c2.UpdatedAt
	firstWithC2, err := repo.UpdateServer(ctx, firstForCopy, &c2)
	if err != nil {
		t.Fatalf("copy-on-write credential update: %v", err)
	}
	if firstWithC2.CredentialID != c2.ID || firstWithC2.CredentialAuthType != c2.AuthType {
		t.Fatalf("first server did not move to c2: %+v", firstWithC2)
	}
	if _, err := repo.GetCredential(ctx, c1Before.ID); err != nil {
		t.Fatalf("c1 removed while second server still references it: %v", err)
	}
	secondAfterCopy, err := repo.GetServer(ctx, second.ID)
	if err != nil || secondAfterCopy.CredentialID != c1Before.ID || secondAfterCopy.CredentialAuthType != c1Before.AuthType {
		t.Fatalf("second server changed during copy-on-write: %+v err=%v", secondAfterCopy, err)
	}

	c2SameID := repositoryCredential(c2.ID, AuthPassword, base.Add(3*time.Minute))
	c2SameID.Envelope = CredentialEnvelope{Nonce: []byte("sole-nonce"), Ciphertext: []byte("sole-ciphertext"), KeyVersion: 3}
	firstWithC2.UpdatedAt = c2SameID.UpdatedAt
	firstWithUpdatedC2, err := repo.UpdateServer(ctx, firstWithC2, &c2SameID)
	if err != nil {
		t.Fatalf("sole-reference same-ID update: %v", err)
	}
	if firstWithUpdatedC2.CredentialAuthType != AuthPassword {
		t.Fatalf("sole-reference auth type=%q want %q", firstWithUpdatedC2.CredentialAuthType, AuthPassword)
	}
	storedC2, err := repo.GetCredential(ctx, c2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(storedC2, c2SameID) {
		t.Fatalf("stored c2=%+v want %+v", storedC2, c2SameID)
	}

	c3 := repositoryCredential("credential-c3", AuthPrivateKey, base.Add(4*time.Minute))
	firstWithUpdatedC2.UpdatedAt = c3.UpdatedAt
	if _, err := repo.UpdateServer(ctx, firstWithUpdatedC2, &c3); err != nil {
		t.Fatalf("replace sole-reference c2 with c3: %v", err)
	}
	if _, err := repo.GetCredential(ctx, c2.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unreferenced c2 error=%v want ErrNotFound", err)
	}
	if _, err := repo.GetCredential(ctx, c1Before.ID); err != nil {
		t.Fatalf("shared c1 removed after unrelated replacement: %v", err)
	}
}

func openRepositoryDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "assets.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func repositoryServer(id, name, credentialID string, now time.Time) Server {
	return Server{
		ID:           id,
		Name:         name,
		Address:      "192.0.2.10",
		Username:     "root",
		CredentialID: credentialID,
		SSHPort:      22,
		Status:       ServerPending,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

func repositoryCredential(id string, authType CredentialAuthType, now time.Time) StoredCredential {
	return StoredCredential{
		ID:        id,
		AuthType:  authType,
		Envelope:  CredentialEnvelope{Nonce: []byte("nonce-" + id), Ciphertext: []byte("ciphertext-" + id), KeyVersion: 1},
		CreatedAt: now,
		UpdatedAt: now,
	}
}
