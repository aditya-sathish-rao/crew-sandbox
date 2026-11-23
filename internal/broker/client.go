package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Revoked is the broker's answer to POST /revoke-all.
type Revoked struct {
	Tasks   int `json:"tasks"`
	Revoked int `json:"revoked"`
}

// RevokeAll asks a running broker to revoke every token it remembers. Tokens are
// memory-only, so a broker that is not running has none to revoke; the caller
// treats that case separately from a broker that answers with an error.
func RevokeAll(ctx context.Context, baseURL string) (Revoked, error) {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		strings.TrimRight(baseURL, "/")+"/revoke-all",
		nil,
	)
	if err != nil {
		return Revoked{}, err
	}
	client := &http.Client{Timeout: 60 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return Revoked{}, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		return Revoked{}, fmt.Errorf(
			"broker /revoke-all: %s: %s",
			response.Status,
			strings.TrimSpace(string(body)),
		)
	}
	var revoked Revoked
	if err := json.NewDecoder(response.Body).Decode(&revoked); err != nil {
		return Revoked{}, fmt.Errorf("broker /revoke-all: %w", err)
	}
	return revoked, nil
}
