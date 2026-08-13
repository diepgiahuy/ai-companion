package idempotency

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const ConflictCode = "IDEMPOTENCY_CONFLICT"

// Request is the durable identity of one externally retriable mutation.
// Actor is the authenticated business actor (not a device/session), Operation
// is a stable product operation name, Key is the caller/tool idempotency token,
// and RequestHash is the canonical semantic request hash.
type Request struct {
	Actor       string
	Operation   string
	Key         string
	RequestHash string
}

func (r Request) Validate() error {
	if strings.TrimSpace(r.Actor) == "" {
		return fmt.Errorf("idempotency actor is required")
	}
	if strings.TrimSpace(r.Operation) == "" {
		return fmt.Errorf("idempotency operation is required")
	}
	if strings.TrimSpace(r.Key) == "" {
		return fmt.Errorf("idempotency key is required")
	}
	if len(r.RequestHash) != sha256.Size*2 {
		return fmt.Errorf("idempotency request hash must be sha256 hex")
	}
	if _, err := hex.DecodeString(r.RequestHash); err != nil {
		return fmt.Errorf("idempotency request hash must be sha256 hex: %w", err)
	}
	return nil
}

// Conflict is stable and safe to surface through the capability boundary. It
// deliberately excludes stored/current payloads so conflicts do not leak data.
type Conflict struct {
	Operation string
	Key       string
}

func (e Conflict) Error() string {
	return ConflictCode + ": idempotency key was already committed with a different request"
}

func IsConflict(err error) bool {
	var conflict Conflict
	return errors.As(err, &conflict)
}

// HashJSON hashes JSON by meaning rather than byte order/whitespace. Go's JSON
// encoder sorts string map keys, while UseNumber preserves numeric tokens
// without converting through float64. Trailing JSON is rejected.
func HashJSON(raw string) (string, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", fmt.Errorf("decode idempotency payload: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return "", fmt.Errorf("decode idempotency payload: multiple JSON values")
		}
		return "", fmt.Errorf("decode idempotency payload: %w", err)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("canonicalize idempotency payload: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func HashValue(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("marshal idempotency payload: %w", err)
	}
	return HashJSON(string(raw))
}

func EqualHash(a, b string) bool {
	a = strings.ToLower(a)
	b = strings.ToLower(b)
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
