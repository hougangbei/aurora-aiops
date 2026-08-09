package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/hougangbei/aurora-aiops/server/internal/assets"
	"github.com/hougangbei/aurora-aiops/server/internal/response"
)

type createAssetServerRequest struct {
	Name               string                    `json:"name"`
	Address            string                    `json:"address"`
	Username           string                    `json:"username"`
	SSHPort            int                       `json:"sshPort"`
	CredentialAuthType assets.CredentialAuthType `json:"credentialAuthType"`
	Password           string                    `json:"password,omitempty"`
	PrivateKey         string                    `json:"privateKey,omitempty"`
	Passphrase         string                    `json:"passphrase,omitempty"`
	TestConnection     bool                      `json:"testConnection"`
}

type updateAssetServerRequest struct {
	Name               *string                    `json:"name,omitempty"`
	Address            *string                    `json:"address,omitempty"`
	Username           *string                    `json:"username,omitempty"`
	SSHPort            *int                       `json:"sshPort,omitempty"`
	CredentialAuthType *assets.CredentialAuthType `json:"credentialAuthType,omitempty"`
	Password           *string                    `json:"password,omitempty"`
	PrivateKey         *string                    `json:"privateKey,omitempty"`
	Passphrase         *string                    `json:"passphrase,omitempty"`
}

type confirmAssetHostKeyRequest struct {
	Fingerprint string `json:"fingerprint"`
}

type assetServerResponse struct {
	ID                   string                    `json:"id"`
	Name                 string                    `json:"name"`
	Address              string                    `json:"address"`
	Username             string                    `json:"username"`
	HostKeyFingerprint   string                    `json:"hostKeyFingerprint,omitempty"`
	SSHPort              int                       `json:"sshPort"`
	Status               assets.ServerStatus       `json:"status"`
	StatusMessage        string                    `json:"statusMessage,omitempty"`
	OSFamily             string                    `json:"osFamily,omitempty"`
	OSVersion            string                    `json:"osVersion,omitempty"`
	Architecture         string                    `json:"architecture,omitempty"`
	CPUCores             int                       `json:"cpuCores"`
	MemoryBytes          int64                     `json:"memoryBytes"`
	DiskBytes            int64                     `json:"diskBytes"`
	LastSeenAt           *time.Time                `json:"lastSeenAt,omitempty"`
	LastCollectedAt      *time.Time                `json:"lastCollectedAt,omitempty"`
	CreatedAt            time.Time                 `json:"createdAt"`
	UpdatedAt            time.Time                 `json:"updatedAt"`
	CredentialAuthType   assets.CredentialAuthType `json:"credentialAuthType"`
	CredentialConfigured bool                      `json:"credentialConfigured"`
}

type assetSnapshotResponse struct {
	ID            string    `json:"id"`
	ServerID      string    `json:"serverId"`
	OSFamily      string    `json:"osFamily"`
	OSVersion     string    `json:"osVersion"`
	KernelVersion string    `json:"kernelVersion"`
	Architecture  string    `json:"architecture"`
	Hostname      string    `json:"hostname"`
	CPUCores      int       `json:"cpuCores"`
	MemoryBytes   int64     `json:"memoryBytes"`
	DiskBytes     int64     `json:"diskBytes"`
	Load1         float64   `json:"load1"`
	UptimeSeconds int64     `json:"uptimeSeconds"`
	CollectedAt   time.Time `json:"collectedAt"`
}

type assetSoftwareResponse struct {
	Category     string `json:"category"`
	Name         string `json:"name"`
	Version      string `json:"version"`
	Architecture string `json:"architecture"`
	Source       string `json:"source"`
	Status       string `json:"status"`
}

type assetConnectionResponse struct {
	Fingerprint string `json:"fingerprint"`
	Trusted     bool   `json:"trusted"`
	Changed     bool   `json:"changed"`
}

func registerAssetRoutes(group *gin.RouterGroup, svc *assets.Service) {
	servers := group.Group("/assets/servers")
	servers.GET("", func(c *gin.Context) {
		items, err := svc.List(c.Request.Context())
		if err != nil {
			respondAssetError(c, err, nil)
			return
		}
		data := make([]assetServerResponse, 0, len(items))
		for _, item := range items {
			data = append(data, assetServerDTO(item))
		}
		c.JSON(http.StatusOK, response.Success(data))
	})
	servers.POST("", RequireAdmin(), func(c *gin.Context) {
		var request createAssetServerRequest
		if err := decodeAssetJSON(c, &request); err != nil {
			respondAssetError(c, assets.ErrInvalidInput, nil)
			return
		}
		created, err := svc.Create(c.Request.Context(), currentActorName(c), assets.CreateServerInput{
			Name: request.Name, Address: request.Address, Username: request.Username, SSHPort: request.SSHPort,
			AuthType:       request.CredentialAuthType,
			Secret:         assets.CredentialSecret{Password: request.Password, PrivateKey: request.PrivateKey, Passphrase: request.Passphrase},
			TestConnection: request.TestConnection,
		})
		if err != nil {
			respondAssetError(c, err, &created)
			return
		}
		c.JSON(http.StatusCreated, response.Success(assetServerDTO(created)))
	})
	servers.GET("/:id", func(c *gin.Context) {
		server, err := svc.Get(c.Request.Context(), c.Param("id"))
		if err != nil {
			respondAssetError(c, err, nil)
			return
		}
		c.JSON(http.StatusOK, response.Success(assetServerDTO(server)))
	})
	servers.PATCH("/:id", RequireAdmin(), func(c *gin.Context) {
		var request updateAssetServerRequest
		if err := decodeAssetJSON(c, &request); err != nil {
			respondAssetError(c, assets.ErrInvalidInput, nil)
			return
		}
		current, err := svc.Get(c.Request.Context(), c.Param("id"))
		if err != nil {
			respondAssetError(c, err, nil)
			return
		}
		input := assets.UpdateServerInput{
			Name: current.Name, Address: current.Address, Username: current.Username, SSHPort: current.SSHPort,
			AuthType: request.CredentialAuthType,
		}
		if request.Name != nil {
			input.Name = *request.Name
		}
		if request.Address != nil {
			input.Address = *request.Address
		}
		if request.Username != nil {
			input.Username = *request.Username
		}
		if request.SSHPort != nil {
			input.SSHPort = *request.SSHPort
		}
		if request.Password != nil || request.PrivateKey != nil || request.Passphrase != nil {
			input.Secret = &assets.CredentialSecret{}
			if request.Password != nil {
				input.Secret.Password = *request.Password
			}
			if request.PrivateKey != nil {
				input.Secret.PrivateKey = *request.PrivateKey
			}
			if request.Passphrase != nil {
				input.Secret.Passphrase = *request.Passphrase
			}
		}
		updated, err := svc.Update(c.Request.Context(), currentActorName(c), c.Param("id"), input)
		if err != nil {
			respondAssetError(c, err, nil)
			return
		}
		c.JSON(http.StatusOK, response.Success(assetServerDTO(updated)))
	})
	servers.DELETE("/:id", RequireAdmin(), func(c *gin.Context) {
		if err := svc.Delete(c.Request.Context(), currentActorName(c), c.Param("id")); err != nil {
			respondAssetError(c, err, nil)
			return
		}
		c.JSON(http.StatusOK, response.Success(gin.H{"deleted": true}))
	})
	servers.POST("/:id/test-connection", RequireOperator(), func(c *gin.Context) {
		result, err := svc.TestConnection(c.Request.Context(), currentActorName(c), c.Param("id"))
		if err != nil {
			respondAssetConnectionError(c, err, result)
			return
		}
		c.JSON(http.StatusOK, response.Success(assetConnectionDTO(result)))
	})
	servers.POST("/:id/confirm-host-key", RequireAdmin(), func(c *gin.Context) {
		var request confirmAssetHostKeyRequest
		if err := decodeAssetJSON(c, &request); err != nil {
			respondAssetError(c, assets.ErrInvalidInput, nil)
			return
		}
		if err := svc.ConfirmHostKey(c.Request.Context(), currentActorName(c), c.Param("id"), request.Fingerprint); err != nil {
			respondAssetError(c, err, nil)
			return
		}
		c.JSON(http.StatusOK, response.Success(gin.H{"confirmed": true}))
	})
	servers.POST("/:id/collect", RequireOperator(), func(c *gin.Context) {
		snapshot, _, err := svc.Collect(c.Request.Context(), currentActorName(c), c.Param("id"))
		if err != nil {
			respondAssetError(c, err, nil)
			return
		}
		c.JSON(http.StatusOK, response.Success(assetSnapshotDTO(snapshot)))
	})
	servers.GET("/:id/snapshots/latest", func(c *gin.Context) {
		snapshot, err := svc.LatestSnapshot(c.Request.Context(), c.Param("id"))
		if err != nil {
			respondAssetError(c, err, nil)
			return
		}
		c.JSON(http.StatusOK, response.Success(assetSnapshotDTO(snapshot)))
	})
	servers.GET("/:id/software", func(c *gin.Context) {
		items, err := svc.Software(c.Request.Context(), c.Param("id"))
		if err != nil {
			respondAssetError(c, err, nil)
			return
		}
		data := make([]assetSoftwareResponse, 0, len(items))
		for _, item := range items {
			data = append(data, assetSoftwareDTO(item))
		}
		c.JSON(http.StatusOK, response.Success(data))
	})
}

func decodeAssetJSON(c *gin.Context, destination any) error {
	decoder := json.NewDecoder(io.LimitReader(c.Request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON value")
	}
	return nil
}

func respondAssetConnectionError(c *gin.Context, err error, result assets.ConnectionResult) {
	var hostKeyErr *assets.HostKeyError
	if errors.As(err, &hostKeyErr) {
		c.JSON(http.StatusConflict, response.Envelope{
			Code: "SSH_HOST_KEY_CONFIRMATION_REQUIRED", Message: "SSH host key confirmation is required",
			Data: assetConnectionDTO(result),
		})
		return
	}
	respondAssetError(c, err, nil)
}

func respondAssetError(c *gin.Context, err error, created *assets.Server) {
	var hostKeyErr *assets.HostKeyError
	switch {
	case errors.As(err, &hostKeyErr):
		data := gin.H{"fingerprint": hostKeyErr.Actual, "changed": hostKeyErr.Changed}
		if created != nil && created.ID != "" {
			data["server"] = assetServerDTO(*created)
		}
		c.JSON(http.StatusConflict, response.Envelope{Code: "SSH_HOST_KEY_CONFIRMATION_REQUIRED", Message: "SSH host key confirmation is required", Data: data})
	case errors.Is(err, assets.ErrInvalidInput):
		c.JSON(http.StatusBadRequest, response.Failure("INVALID_ARGUMENT", "Invalid asset request"))
	case errors.Is(err, assets.ErrNotFound):
		c.JSON(http.StatusNotFound, response.Failure("ASSET_NOT_FOUND", "Asset server was not found"))
	case errors.Is(err, assets.ErrNameConflict):
		c.JSON(http.StatusConflict, response.Failure("ASSET_NAME_CONFLICT", "Asset server name already exists"))
	case errors.Is(err, assets.ErrEncryptionUnavailable):
		c.JSON(http.StatusServiceUnavailable, response.Failure("ASSET_ENCRYPTION_UNAVAILABLE", "Asset credential encryption is unavailable"))
	case errors.Is(err, assets.ErrActiveTask):
		c.JSON(http.StatusConflict, response.Failure("ASSET_ACTIVE_TASK", "Asset server has an active task"))
	case errors.Is(err, assets.ErrHostKeyUntrusted), errors.Is(err, assets.ErrHostKeyChanged):
		c.JSON(http.StatusConflict, response.Failure("SSH_HOST_KEY_CONFIRMATION_REQUIRED", "SSH host key confirmation is required"))
	default:
		c.JSON(http.StatusInternalServerError, response.Failure("ASSET_INTERNAL_ERROR", "Asset operation failed"))
	}
}

func assetServerDTO(server assets.Server) assetServerResponse {
	return assetServerResponse{
		ID: server.ID, Name: server.Name, Address: server.Address, Username: server.Username,
		HostKeyFingerprint: server.HostKeyFingerprint, SSHPort: server.SSHPort, Status: server.Status,
		StatusMessage: server.StatusMessage, OSFamily: server.OSFamily, OSVersion: server.OSVersion,
		Architecture: server.Architecture, CPUCores: server.CPUCores, MemoryBytes: server.MemoryBytes,
		DiskBytes: server.DiskBytes, LastSeenAt: server.LastSeenAt, LastCollectedAt: server.LastCollectedAt,
		CreatedAt: server.CreatedAt, UpdatedAt: server.UpdatedAt, CredentialAuthType: server.CredentialAuthType,
		CredentialConfigured: server.CredentialConfigured,
	}
}

func assetSnapshotDTO(snapshot assets.Snapshot) assetSnapshotResponse {
	return assetSnapshotResponse{
		ID: snapshot.ID, ServerID: snapshot.ServerID, OSFamily: snapshot.OSFamily, OSVersion: snapshot.OSVersion,
		KernelVersion: snapshot.KernelVersion, Architecture: snapshot.Architecture, Hostname: snapshot.Hostname,
		CPUCores: snapshot.CPUCores, MemoryBytes: snapshot.MemoryBytes, DiskBytes: snapshot.DiskBytes,
		Load1: snapshot.Load1, UptimeSeconds: snapshot.UptimeSeconds, CollectedAt: snapshot.CollectedAt,
	}
}

func assetSoftwareDTO(item assets.SoftwareItem) assetSoftwareResponse {
	return assetSoftwareResponse{Category: item.Category, Name: item.Name, Version: item.Version, Architecture: item.Architecture, Source: item.Source, Status: item.Status}
}

func assetConnectionDTO(result assets.ConnectionResult) assetConnectionResponse {
	return assetConnectionResponse{Fingerprint: result.Fingerprint, Trusted: result.Trusted, Changed: result.Changed}
}
