package assets

import "time"

type ServerStatus string

const (
	ServerPending ServerStatus = "pending"
	ServerOnline  ServerStatus = "online"
	ServerOffline ServerStatus = "offline"
	ServerError   ServerStatus = "error"
)

type CredentialAuthType string

const (
	AuthPassword   CredentialAuthType = "password"
	AuthPrivateKey CredentialAuthType = "private_key"
)

type CredentialSecret struct {
	Password   string
	PrivateKey string
	Passphrase string
}

type CredentialEnvelope struct {
	Nonce      []byte
	Ciphertext []byte
	KeyVersion int
}

type StoredCredential struct {
	ID        string
	AuthType  CredentialAuthType
	Envelope  CredentialEnvelope
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Server struct {
	ID                   string
	Name                 string
	Address              string
	Username             string
	CredentialID         string `json:"-"`
	HostKeyFingerprint   string
	SSHPort              int
	Status               ServerStatus
	StatusMessage        string
	OSFamily             string
	OSVersion            string
	Architecture         string
	CPUCores             int
	MemoryBytes          int64
	DiskBytes            int64
	LastSeenAt           *time.Time
	LastCollectedAt      *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
	CredentialAuthType   CredentialAuthType
	CredentialConfigured bool
}

type CreateServerInput struct {
	Name           string
	Address        string
	Username       string
	SSHPort        int
	AuthType       CredentialAuthType
	Secret         CredentialSecret
	TestConnection bool
}

type UpdateServerInput struct {
	Name     string
	Address  string
	Username string
	SSHPort  int
	AuthType *CredentialAuthType
	Secret   *CredentialSecret
}

type ConnectionResult struct {
	Fingerprint string `json:"fingerprint"`
	Trusted     bool   `json:"trusted"`
	Changed     bool   `json:"changed"`
}

type Snapshot struct {
	ID            string
	ServerID      string
	OSFamily      string
	OSVersion     string
	KernelVersion string
	Architecture  string
	Hostname      string
	CPUCores      int
	MemoryBytes   int64
	DiskBytes     int64
	Load1         float64
	UptimeSeconds int64
	CollectedAt   time.Time
}

type SoftwareItem struct {
	Category     string
	Name         string
	Version      string
	Architecture string
	Source       string
	Status       string
}
