package tunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Credentials are what the registry hands a node so it can run its own tunnel.
//
// The token authorizes running one named tunnel and nothing else. It is not a
// Cloudflare API key: the platform's actual Cloudflare credentials never leave
// the registry, and a provider never has to have a Cloudflare account at all.
type Credentials struct {
	Token           string `json:"tunnel_token"`
	SSHHostname     string `json:"ssh_hostname"`
	JupyterHostname string `json:"jupyter_hostname"`
	APIHostname     string `json:"api_hostname"`
}

// Fetch asks the registry to provision this node's tunnel and return its token.
//
// This is the one call in the leasing flow that needs the registry to be up,
// and it happens once per node rather than once per lease: the result is cached
// in config.yaml. A node that already has a token never calls this again, so a
// registry outage cannot interrupt an established provider's leases — the same
// discipline as the heartbeat never being allowed to block a paid job.
//
// The request is authorized with the node's registry token, the same listing
// credential heartbeats use. It cannot move funds and it is not key material.
func Fetch(ctx context.Context, registryURL, nodeID, registryToken string) (*Credentials, error) {
	if registryURL == "" {
		return nil, fmt.Errorf("this node is not listed on a registry, so there is nobody to provision a tunnel with; "+
			"set leases.tunnel.mode to %q to run without a Cloudflare account", "quick")
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	url := strings.TrimRight(registryURL, "/") + "/v1/nodes/" + nodeID + "/tunnel-token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+registryToken)

	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ask %s for a tunnel token: %w", registryURL, err)
	}
	defer response.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<16))
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry answered %s: %s", response.Status, strings.TrimSpace(string(body)))
	}

	var creds Credentials
	if err := json.Unmarshal(body, &creds); err != nil {
		return nil, fmt.Errorf("decode tunnel credentials: %w", err)
	}
	if creds.Token == "" || creds.JupyterHostname == "" {
		return nil, fmt.Errorf("registry returned an incomplete tunnel credential")
	}
	return &creds, nil
}
