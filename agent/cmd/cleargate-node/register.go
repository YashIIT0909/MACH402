package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/YashIIT0909/ClearGate/agent/internal/config"
)

// newRegisterCommand registers this node's ERC-8004 identity.
//
// Separate from `serve`, and deliberately so. An identity is a claim a provider
// makes once, and one that appeared as a side effect of starting a daemon would
// be a claim nobody chose to make. It is also the only command that spends HBAR
// on the provider's behalf, which is not something to do without being asked.
func newRegisterCommand() *cobra.Command {
	var registryContract string

	cmd := &cobra.Command{
		Use:   "register",
		Short: "Register this node's on-chain provider identity (ERC-8004)",
		Long: "Registers this node as an agent in the ERC-8004 identity registry on Hedera, " +
			"giving it a persistent id that a renter's agent can resolve independently of " +
			"ClearGate's own registry.\n\n" +
			"Safe to re-run: if this node already has an identity it is reported rather than " +
			"replaced. Registering twice would orphan the first id.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			configPath, _ := cmd.Flags().GetString("config")
			return register(cmd.Context(), configPath, registryContract)
		},
	}

	cmd.Flags().StringVar(&registryContract, "registry-contract", "",
		"IdentityRegistry contract id or EVM address (defaults to identity.registry_contract_id)")
	return cmd
}

func register(ctx context.Context, configPath, registryContract string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	if !cfg.Hedera.Enabled {
		return errors.New(
			"this node has no operator key yet — run `cleargate-node setup --enable-hcs` first, " +
				"which creates and funds the key an identity is bound to")
	}
	if registryContract == "" {
		registryContract = cfg.Identity.RegistryContractID
	}
	if registryContract == "" {
		return errors.New(
			"no identity registry configured — pass --registry-contract, or set " +
				"identity.registry_contract_id in config.yaml")
	}

	// The agent domain is the host of public_url, because that is where the
	// agent card is served. An identity pointing at an address nobody can reach
	// resolves to nothing, so this is refused rather than registered.
	domain, err := agentDomain(cfg.PublicURL)
	if err != nil {
		return err
	}

	sidecar := newSidecar(cfg)
	if err := sidecar.Available(); err != nil {
		return err
	}

	fmt.Printf("node        %s\n", cfg.NodeID)
	fmt.Printf("domain      %s\n", domain)
	fmt.Printf("registry    %s\n", registryContract)

	keyCtx, cancelKey := context.WithTimeout(ctx, 60*time.Second)
	key, err := sidecar.EnsureKey(keyCtx)
	cancelKey()
	if err != nil {
		return fmt.Errorf("could not load this node's operator key: %w", err)
	}
	fmt.Printf("address     %s\n", key.EVMAddress)

	if !key.Funded {
		return fmt.Errorf(
			"the operator key at %s has no Hedera account yet.\n"+
				"Send a few HBAR to %s to create it, then run this again",
			cfg.Hedera.OperatorKeyPath, key.EVMAddress)
	}

	// The sidecar checks the contract for an existing registration before
	// minting, so this is idempotent at two levels — config and chain. A
	// provider who lost their config.yaml recovers their identity here rather
	// than silently acquiring a second one.
	if cfg.Identity.AgentID != 0 {
		fmt.Printf("\nThis node is already registered as agent %d.\n", cfg.Identity.AgentID)
		fmt.Println("Re-running will confirm that registration rather than create a second one.")
	}

	registerCtx, cancelRegister := context.WithTimeout(ctx, 3*time.Minute)
	defer cancelRegister()

	fmt.Println("\nregistering ...")
	registration, err := sidecar.RegisterIdentity(registerCtx, registryContract, domain)
	if err != nil {
		return fmt.Errorf("could not register this node's identity: %w", err)
	}

	agentID, err := strconv.ParseUint(registration.AgentID, 10, 64)
	if err != nil {
		return fmt.Errorf("the registry returned an agent id that is not a number (%q): %w",
			registration.AgentID, err)
	}

	cfg.Identity.AgentID = agentID
	cfg.Identity.AgentAddress = registration.AgentAddress
	cfg.Identity.RegistryContractID = registryContract
	if err := config.Save(configPath, cfg); err != nil {
		return fmt.Errorf("registered as agent %d, but could not record it in %s: %w",
			agentID, configPath, err)
	}

	if registration.Created {
		fmt.Printf("\nregistered  agent %d\n", agentID)
	} else {
		fmt.Printf("\nconfirmed   agent %d (already registered)\n", agentID)
	}
	fmt.Printf("address     %s\n", registration.AgentAddress)
	fmt.Printf("transaction %s\n", registration.Transaction)
	fmt.Printf("card        %s/.well-known/agent-card.json\n", strings.TrimRight(cfg.PublicURL, "/"))
	fmt.Printf("hashscan    https://hashscan.io/testnet/contract/%s\n", registryContract)
	fmt.Println("\nThe next heartbeat publishes this id to the registry. Nothing else to do.")
	return nil
}

// agentDomain is the host of public_url — where the agent card lives.
//
// Rejects an unset or non-absolute public_url rather than registering an
// identity that resolves nowhere: the whole value of the on-chain record is
// that it points at something a stranger can fetch.
func agentDomain(publicURL string) (string, error) {
	if publicURL == "" {
		return "", errors.New(
			"public_url is not set, so there is no address for this identity to point at — " +
				"set it in config.yaml first")
	}

	parsed, err := url.Parse(publicURL)
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("public_url %q is not an absolute http(s) URL", publicURL)
	}
	return parsed.Host, nil
}
