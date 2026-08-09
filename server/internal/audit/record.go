package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hougangbei/aurora-aiops/server/internal/evidence"
)

// Record is one append-only audit entry. Hash chains every record to the
// previous one so any tampering is detectable.
type Record struct {
	ID        int64     `json:"id"`
	Actor     string    `json:"actor"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	Result    string    `json:"result"`
	Payload   string    `json:"payload"`
	Timestamp time.Time `json:"timestamp"`
	Hash      string    `json:"hash"`
}

// chainBody is the canonical, hash-free projection of a record used as the
// hashing input. Field order is fixed, so json.Marshal is deterministic.
type chainBody struct {
	ID        int64     `json:"id"`
	Actor     string    `json:"actor"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	Result    string    `json:"result"`
	Payload   string    `json:"payload"`
	Timestamp time.Time `json:"timestamp"`
}

func (r Record) chainBody() ([]byte, error) {
	return json.Marshal(chainBody{
		ID:        r.ID,
		Actor:     r.Actor,
		Action:    r.Action,
		Target:    r.Target,
		Result:    r.Result,
		Payload:   r.Payload,
		Timestamp: r.Timestamp.UTC(),
	})
}

// Hash computes SHA256(previous_hash || canonical_json(record_without_hash)).
func Hash(previousHash string, record Record) string {
	body, err := record.chainBody()
	if err != nil {
		return ""
	}
	input := make([]byte, 0, len(previousHash)+len(body))
	input = append(input, previousHash...)
	input = append(input, body...)
	sum := sha256.Sum256(input)
	return hex.EncodeToString(sum[:])
}

var ErrHashMismatch = errors.New("audit hash mismatch")

// Verify checks the hash chain of all records in order. It returns ErrHashMismatch
// when any record was modified after it was appended.
func Verify(records []Record) error {
	previousHash := ""
	for _, record := range records {
		want := Hash(previousHash, record)
		if record.Hash != want {
			return fmt.Errorf("%w: record %d", ErrHashMismatch, record.ID)
		}
		previousHash = record.Hash
	}
	return nil
}

// RedactPayload masks credential-like material (tokens, API keys, secrets)
// before a payload is persisted. It reuses the evidence redactor so audit
// records never carry real credentials.
func RedactPayload(payload string) string {
	return evidence.RedactSecrets(payload)
}
