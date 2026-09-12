package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/YashIIT0909/ClearGate/agent/internal/config"
	"github.com/YashIIT0909/ClearGate/agent/internal/escrow"
	"github.com/YashIIT0909/ClearGate/agent/internal/hedera"
	"github.com/YashIIT0909/ClearGate/agent/internal/runner"
	"github.com/YashIIT0909/ClearGate/agent/internal/sshca"
	"github.com/YashIIT0909/ClearGate/agent/internal/x402"
)

func newSetupCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Write a config.yaml and check this machine can run jobs",
		Long: "Prompts for the Hedera account that should receive payment, then " +
			"preflights Docker and the GPU so problems surface now rather than " +
			"during someone's paid job.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts := setupOptions{}
			opts.configPath, _ = cmd.Flags().GetString("config")
			opts.payTo, _ = cmd.Flags().GetString("pay-to")
			opts.price, _ = cmd.Flags().GetString("price-tinybars")
			opts.registryURL, _ = cmd.Flags().GetString("registry-url")
			opts.publicURL, _ = cmd.Flags().GetString("public-url")
			opts.gpu, _ = cmd.Flags().GetBool("gpu")
			opts.force, _ = cmd.Flags().GetBool("force")
			opts.leases, _ = cmd.Flags().GetBool("enable-leases")
			opts.leasePrice, _ = cmd.Flags().GetString("lease-price-tinybars-per-minute")
			opts.tunnelMode, _ = cmd.Flags().GetString("tunnel-mode")
			opts.hcs, _ = cmd.Flags().GetBool("enable-hcs")
			opts.sessions, _ = cmd.Flags().GetBool("enable-sessions")
			opts.identityContract, _ = cmd.Flags().GetString("identity-contract")
			opts.selfSettle, _ = cmd.Flags().GetBool("self-settle")
			opts.hederaSidecar, _ = cmd.Flags().GetString("hedera-sidecar")
			return setup(cmd.Context(), opts)
		},
	}
	cmd.Flags().String("pay-to", "", "Hedera account that receives payment, e.g. 0.0.1234")
	cmd.Flags().String("price-tinybars", "", "flat price per job in tinybars (100000 = 0.001 HBAR)")
	cmd.Flags().String("registry-url", "", "registry to list this node on; empty keeps it unlisted")
	cmd.Flags().String("public-url", "", "how renters reach this node, e.g. http://1.2.3.4:8402 (required with --registry-url)")
	cmd.Flags().Bool("gpu", false, "offer GPU passthrough (requires the NVIDIA Container Toolkit)")
	// Deliberately separate from --gpu. Letting a stranger open a shell on your
	// machine is a bigger ask than running their sandboxed batch job, even
	// isolated, and it must never be switched on as a side effect of something
	// else (implementation.md §8.2).
	cmd.Flags().Bool("enable-leases", false,
		"also sell timed interactive access — an SSH shell and a Jupyter server in a container on this machine")
	cmd.Flags().String("lease-price-tinybars-per-minute", "",
		"price of one minute of interactive time, in tinybars (only with --enable-leases)")
	cmd.Flags().String("tunnel-mode", "",
		"how renters reach a lease: \"quick\" (no Cloudflare account, Jupyter only) or \"named\" (registry-provisioned, adds SSH)")
	// Both of these create a node-local Hedera key, which is the first time
	// anything in this repo puts key material on a provider's machine. That is
	// why they are explicit flags rather than implied by anything else.
	cmd.Flags().Bool("enable-hcs", false,
		"publish every settlement to a Hedera Consensus Service topic you own, so earnings can be audited without trusting us")
	cmd.Flags().Bool("enable-sessions", false,
		"sell interactive time as a metered credit, refunding whatever a renter does not burn (requires --enable-hcs)")
	cmd.Flags().String("identity-contract", "",
		"IdentityRegistry contract for `cleargate-node register`")
	cmd.Flags().Bool("self-settle", false,
		"pay session refunds from this node's operator account automatically, instead of leaving them to be paid by hand")
	// A bare command name is resolved against the invoking shell's PATH every
	// time the node starts — fine for a developer's own terminal, fragile for
	// anything unattended (a systemd unit, a fresh terminal that never sourced
	// the shell profile again). Persisting an absolute path here removes that
	// dependency for the life of this config, which is what the installer uses
	// this for: it knows exactly where it just put the binary.
	cmd.Flags().String("hedera-sidecar", "",
		"absolute path to the cleargate-hedera binary, instead of resolving it from PATH at every startup")
	cmd.Flags().Bool("force", false, "overwrite an existing config")
	return cmd
}

// setupOptions is what the install script passes in non-interactively, and what
// the prompts fill in when a provider runs setup by hand.
type setupOptions struct {
	configPath  string
	payTo       string
	price       string
	registryURL string
	publicURL   string
	gpu         bool
	force       bool
	leases      bool
	leasePrice  string
	tunnelMode  string

	hcs              bool
	sessions         bool
	identityContract string
	selfSettle       bool
	hederaSidecar    string
}

func setup(ctx context.Context, opts setupOptions) error {
	path := opts.configPath
	if _, err := os.Stat(path); err == nil && !opts.force {
		return fmt.Errorf("%s already exists; pass --force to overwrite it", path)
	}

	cfg := config.Default()
	cfg.GPUEnabled = opts.gpu
	cfg.Leases.Enabled = opts.leases
	if opts.leasePrice != "" {
		if _, err := strconv.ParseUint(strings.TrimSpace(opts.leasePrice), 10, 64); err != nil {
			return fmt.Errorf("lease price must be a whole number of tinybars per minute: %w", err)
		}
		cfg.Leases.PriceTinybarsPerMinute = strings.TrimSpace(opts.leasePrice)
	}
	if opts.tunnelMode != "" {
		cfg.Leases.Tunnel.Mode = strings.TrimSpace(opts.tunnelMode)
	}

	// Sessions need the sidecar to publish the refund-owed trail and, with
	// --self-settle, to pay the refund; HCS needs it to publish at all. Either
	// implies the Hedera block is on, so a provider does not have to know that
	// and pass a third flag.
	cfg.Hedera.Enabled = opts.hcs || opts.sessions
	cfg.HCS.Enabled = opts.hcs
	if opts.hederaSidecar != "" {
		cfg.Hedera.Sidecar = strings.TrimSpace(opts.hederaSidecar)
	}
	cfg.Identity.RegistryContractID = strings.TrimSpace(opts.identityContract)
	if opts.sessions {
		cfg.Leases.PaymentMode = config.PaymentSession
		cfg.Leases.SelfSettle = opts.selfSettle
		if !cfg.Leases.Enabled {
			return errors.New("--enable-sessions only applies to interactive time; pass --enable-leases too")
		}
		// Refused here rather than at the first sale. The running refund-owed
		// trail is what makes prepaying a stranger checkable, and a session node
		// without it is selling a promise with nothing behind it.
		if !cfg.HCS.Enabled {
			return errors.New("--enable-sessions needs --enable-hcs: the audit topic is where the node " +
				"publishes what it owes a renter while their session runs")
		}
	}

	reader := bufio.NewReader(os.Stdin)

	payTo := opts.payTo
	if payTo == "" {
		payTo = prompt(reader, "Hedera account to be paid into (e.g. 0.0.1234): ")
	}
	cfg.PayTo = strings.TrimSpace(payTo)

	price := opts.price
	if price == "" {
		entered := prompt(reader, fmt.Sprintf("Price per job in tinybars [%s]: ", cfg.PriceTinybars))
		if entered != "" {
			price = entered
		}
	}
	if price != "" {
		if _, err := strconv.ParseUint(strings.TrimSpace(price), 10, 64); err != nil {
			return fmt.Errorf("price must be a whole number of tinybars: %w", err)
		}
		cfg.PriceTinybars = strings.TrimSpace(price)
	}

	cfg.NodeID = newNodeID()

	// Listing is opt-in. A node with no registry_url still sells jobs to any
	// renter who knows its URL; it just does not appear on the website.
	cfg.RegistryURL = strings.TrimRight(strings.TrimSpace(opts.registryURL), "/")
	cfg.PublicURL = strings.TrimRight(strings.TrimSpace(opts.publicURL), "/")
	if cfg.RegistryURL != "" {
		if cfg.PublicURL == "" {
			cfg.PublicURL = prompt(reader, "Public URL renters use to reach this node (e.g. http://1.2.3.4:8402): ")
			cfg.PublicURL = strings.TrimRight(strings.TrimSpace(cfg.PublicURL), "/")
		}
		// Proves to the registry that later heartbeats come from this same node,
		// so nobody else can repoint the listing at their own machine.
		cfg.RegistryToken = newRegistryToken()
	}

	// Lease state lives beside config.yaml, not beside whatever directory setup
	// happened to be run from — otherwise a node started from elsewhere would
	// generate a second certificate authority and invalidate every certificate
	// it had already issued.
	configDir := filepath.Dir(path)
	cfg.Leases.CAKeyPath = filepath.Join(configDir, "lease-ca")
	cfg.Leases.Tunnel.ConfigDir = filepath.Join(configDir, "cloudflared")
	cfg.Hedera.OperatorKeyPath = filepath.Join(configDir, "hedera-operator")

	if err := cfg.ValidateForSetup(); err != nil {
		return err
	}

	fmt.Println("\npreflight")

	// A node that cannot reach the facilitator cannot be paid.
	fac := x402.NewFacilitator(cfg.FacilitatorURL, 20*time.Second)
	kind, err := fac.Kind(ctx, x402.SchemeExact, cfg.Network)
	if err != nil {
		return fmt.Errorf("  facilitator: %w", err)
	}
	feePayer, _ := kind.FeePayer()
	fmt.Printf("  facilitator  %s (fee payer %s)\n", cfg.FacilitatorURL, feePayer)

	// A node that cannot reach Docker cannot run anything.
	docker, err := runner.NewDocker(cfg.DockerHost)
	if err != nil {
		return fmt.Errorf("  docker: %w", err)
	}
	if err := docker.Ping(ctx); err != nil {
		return fmt.Errorf("  docker: %w\n  is the daemon running, and is your user in the docker group?", err)
	}
	info, err := docker.Info(ctx)
	if err != nil {
		return fmt.Errorf("  docker: %w", err)
	}
	fmt.Printf("  docker       %s\n", info.ServerVersion)

	detected := runner.DetectGPU(ctx, docker, cfg.GPUEnabled)
	if detected.Available {
		fmt.Printf("  gpu          %s (%d MB)\n", detected.Model, detected.VRAMMb)
	} else {
		fmt.Printf("  gpu          CPU-fallback mode — %s\n", detected.Reason)
	}

	if cfg.Leases.Enabled {
		if err := preflightLeases(ctx, &cfg, docker); err != nil {
			return err
		}
	}

	if cfg.Hedera.Enabled {
		funded, err := preflightHedera(ctx, &cfg)
		if err != nil {
			return err
		}
		if !funded {
			return nil
		}
	}

	// A registry that is unreachable is worth knowing about now, while the
	// provider is still watching, rather than as a node that quietly never
	// appears on the website. It is not fatal: the node sells jobs regardless.
	if cfg.RegistryURL != "" {
		if err := pingRegistry(ctx, cfg.RegistryURL); err != nil {
			fmt.Printf("  registry     unreachable — %v\n", err)
			fmt.Printf("               the node will keep retrying once it starts\n")
		} else {
			fmt.Printf("  registry     %s\n", cfg.RegistryURL)
		}
	}

	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := config.Save(path, cfg); err != nil {
		return err
	}

	fmt.Printf("\nwrote %s\n", path)
	fmt.Printf("node id      %s\n", cfg.NodeID)
	fmt.Printf("paid into    %s\n", cfg.PayTo)
	fmt.Printf("price        %s tinybars per job\n", cfg.PriceTinybars)
	if cfg.RegistryURL != "" {
		fmt.Printf("listed on    %s as %s\n", cfg.RegistryURL, cfg.PublicURL)
	} else {
		fmt.Printf("listed on    nothing — this node is private\n")
	}
	if cfg.Leases.Enabled {
		fmt.Printf("leases       on, %s tinybars per minute, %s tunnel\n",
			cfg.Leases.PriceTinybarsPerMinute, cfg.Leases.Tunnel.Mode)
		fmt.Printf("             renters get an SSH shell and a Jupyter server in a container here\n")
	} else {
		fmt.Printf("leases       off — this node sells batch jobs only\n")
	}
	fmt.Printf("\nstart it with:  cleargate-node serve --config %s\n", path)
	return nil
}

// preflightLeases checks everything interactive access needs, and generates the
// node's certificate authority while the provider is still watching.
//
// Each of these fails at a bad moment if it is left to discover itself: a
// missing ssh-keygen surfaces as `exec: "ssh-keygen": executable file not found`
// in the middle of a renter's paid request, and a missing lease image surfaces
// as a lease that cannot be provisioned inside the renter's payment window. All
// of it is knowable now.
func preflightLeases(ctx context.Context, cfg *config.Config, docker *runner.Docker) error {
	// The CA. Generated once, here; the private half never leaves this machine
	// and is never copied anywhere, which is the same custody rule that keeps
	// this binary free of a Hedera key (CLAUDE.md invariant 1).
	ca, err := sshca.Ensure(ctx, cfg.Leases.CAKeyPath, "cleargate-"+cfg.NodeID)
	if err != nil {
		return fmt.Errorf("  ssh ca: %w\n  ssh-keygen is required when leases are enabled", err)
	}
	fmt.Printf("  ssh ca       %s\n", cfg.Leases.CAKeyPath)
	fmt.Printf("               %s\n", truncateKey(ca.PublicKey()))

	// cloudflared is how a renter reaches this machine at all. "off" is the one
	// mode that does not need it, and it only makes sense on a LAN.
	if cfg.Leases.Tunnel.Mode != config.TunnelOff {
		binary := cfg.Leases.Tunnel.Binary
		if _, err := exec.LookPath(binary); err != nil {
			return fmt.Errorf("  tunnel: %q is not installed, and renters cannot reach a lease without it.\n"+
				"  Install it from https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/,\n"+
				"  or re-run with --tunnel-mode off if this machine is already directly reachable", binary)
		}
		fmt.Printf("  tunnel       %s, %s mode\n", binary, cfg.Leases.Tunnel.Mode)
		if cfg.Leases.Tunnel.Mode == config.TunnelQuick {
			fmt.Printf("               Jupyter only — named mode adds an SSH terminal\n")
		}
		if cfg.Leases.Tunnel.Mode == config.TunnelNamed && cfg.RegistryURL == "" {
			return fmt.Errorf("  tunnel: named mode needs a registry to provision the tunnel; " +
				"pass --registry-url, or use --tunnel-mode quick")
		}
	}

	// A lease settles only once its container is up and reachable, so there is
	// no staging phase to hide an image pull in. The image has to be here first.
	for _, image := range []string{cfg.Leases.Image, cfg.Leases.Egress.ProxyImage} {
		if docker.HasImage(ctx, image) {
			fmt.Printf("  lease image  %s\n", image)
			continue
		}
		fmt.Printf("  lease image  %s is missing — run `make lease-image` before selling leases\n", image)
	}

	// A provider who believes they are renting out a GPU and is actually selling
	// CPU time will hear about it from an angry renter. Say it here instead.
	if detected := runner.DetectGPU(ctx, docker, cfg.GPUEnabled); detected.Available {
		if cudaCapable(ctx, docker, cfg.Leases.Image) {
			fmt.Printf("  lease gpu    renters can use %s\n", detected.Model)
		} else {
			fmt.Printf("  lease gpu    NOT usable — %s is passed through, but the lease image\n", detected.Model)
			fmt.Printf("               has no CUDA runtime, so a renter's container cannot compute\n")
			fmt.Printf("               on it. Rebuild with `make lease-image` on this machine;\n")
			fmt.Printf("               until then this node sells leases as CPU-only.\n")
		}
	}

	return nil
}

// cudaCapable mirrors the runner's check, so setup and serve agree about
// whether a lease on this image can use the card.
func cudaCapable(ctx context.Context, docker *runner.Docker, image string) bool {
	env, err := docker.ImageEnv(ctx, image)
	if err != nil {
		return false
	}
	for _, entry := range env {
		name, value, _ := strings.Cut(entry, "=")
		if name == "CUDA_VERSION" {
			return true
		}
		// "utility" alone runs nvidia-smi and nothing else — exactly the trap.
		if name == "NVIDIA_DRIVER_CAPABILITIES" && strings.Contains(value, "compute") {
			return true
		}
	}
	return false
}

// truncateKey shortens a public key for display. The whole thing is public, but
// a full line of base64 in the middle of a setup summary reads as noise.
func truncateKey(key string) string {
	if len(key) <= 56 {
		return key
	}
	return key[:53] + "..."
}

// pingRegistry checks the registry is up. It only ever reports discovery
// health: the registry never touches money (CLAUDE.md invariant 3).
func pingRegistry(ctx context.Context, baseURL string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health", nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("answered %s", response.Status)
	}
	return nil
}

func prompt(reader *bufio.Reader, question string) string {
	fmt.Print(question)
	line, err := reader.ReadString('\n')
	if err != nil {
		return ""
	}
	return strings.TrimSpace(line)
}

func newNodeID() string {
	var raw [8]byte
	_, _ = rand.Read(raw[:])
	return "node_" + hex.EncodeToString(raw[:])
}

// newRegistryToken mints this node's listing credential. It authorizes exactly
// one thing — updating this node's own row in the registry — and can never
// authorize a payment.
func newRegistryToken() string {
	var raw [32]byte
	_, _ = rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
}

// preflightHedera creates the node's operator key and audit topic.
//
// Mirrors preflightLeases, and for the same reason: everything that can fail
// should fail here, while a provider is watching and can act on it, rather than
// in the middle of someone else's paid session.
//
// This is the first and only place ClearGate writes key material to a
// provider's disk. It is worth being clear about what that key is and is not:
// it pays its own fees and signs topic messages and contract calls, and it is
// NOT pay_to. Earnings accumulate in pay_to, which still signs nothing, so a
// compromise of this key costs the HBAR sitting in it and nothing else.
// hederaFundingTimeout bounds how long setup waits for a provider to fund the
// operator key before giving up and falling back to "re-run me by hand."
// Long enough to cover opening a wallet app and sending a transfer, short
// enough that an unattended install script does not hang indefinitely.
const hederaFundingTimeout = 10 * time.Minute

// hederaFundingPollInterval matches the cadence a renter would notice funding
// land at — fast enough to feel immediate, slow enough not to hammer the
// mirror node while nothing has happened yet.
const hederaFundingPollInterval = 5 * time.Second

// waitForFunding polls the mirror node until the operator's address resolves
// to a real account, or the timeout elapses.
//
// This is what turns "fund this address, then re-run setup" into a single
// sitting: a provider sends HBAR from their phone and setup notices on its
// own, the same way the node itself never assumes a payment happened without
// checking. Returns nil, nil on a timeout — not an error, since not funding
// the key yet is a choice a provider is allowed to make.
func waitForFunding(ctx context.Context, sidecar *hedera.Sidecar, evmAddress string) (*hedera.AccountInfo, error) {
	deadline := time.Now().Add(hederaFundingTimeout)
	ticker := time.NewTicker(hederaFundingPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			account, err := sidecar.AccountInfo(checkCtx, evmAddress)
			cancel()
			if err == nil && account.Found {
				fmt.Printf("               funded\n")
				return account, nil
			}
			if time.Now().After(deadline) {
				return nil, nil
			}
			fmt.Print(".")
		}
	}
}

func preflightHedera(ctx context.Context, cfg *config.Config) (bool, error) {
	sidecar := newSidecar(*cfg)
	if err := sidecar.Available(); err != nil {
		return false, fmt.Errorf("  hedera: %w", err)
	}

	keyCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	key, err := sidecar.EnsureKey(keyCtx)
	cancel()
	if err != nil {
		return false, fmt.Errorf("  hedera: %w", err)
	}

	fmt.Printf("  operator     %s\n", cfg.Hedera.OperatorKeyPath)
	fmt.Printf("               %s\n", key.EVMAddress)

	if !key.Funded {
		// Not an error. An ECDSA key has an address from the moment it is
		// generated, but no Hedera account exists until someone sends HBAR to
		// it — so "unfunded" is a step the provider has not taken yet, not a
		// failure of setup.
		//
		// Rather than making that a second manual step — fund it, then remember
		// to come back and re-run this whole command — setup waits here and
		// polls the mirror node itself. A provider funding it from their phone
		// finishes the entire install in one sitting instead of two.
		fmt.Printf("               no account yet — send a few HBAR to that address to create it\n")
		fmt.Printf("               (https://portal.hedera.com on testnet)\n")
		fmt.Printf("               waiting for it to arrive — ctrl-c to stop and configure without Hedera features\n")

		account, err := waitForFunding(ctx, sidecar, key.EVMAddress)
		if err != nil {
			return false, fmt.Errorf("  hedera: %w", err)
		}
		if account == nil {
			fmt.Printf("               still nothing after %s — fund it and re-run setup when you have\n",
				hederaFundingTimeout)
			return false, nil
		}
		key.AccountID = account.AccountID
		key.BalanceTinybars = account.BalanceTinybars
	}

	cfg.Hedera.OperatorAccountID = key.AccountID
	fmt.Printf("               %s, balance %s tinybars\n", key.AccountID, key.BalanceTinybars)

	// A session node with self_settle on pays refunds out of the operator
	// account, which is a genuine change to what that key is for: it used to
	// hold a fee float and nothing else. Checked here, because a provider who
	// discovers it when a renter is owed money discovers it far too late.
	//
	// Earnings are untouched by this. They land in pay_to, which still signs
	// nothing and whose key this machine still does not have.
	if cfg.Leases.PaymentMode == config.PaymentSession && cfg.Leases.SelfSettle {
		chunk, err := sessionChunkCost(cfg)
		if err != nil {
			return false, fmt.Errorf("  sessions: %w", err)
		}
		balance, ok := new(big.Int).SetString(key.BalanceTinybars, 10)
		if !ok {
			balance = big.NewInt(0)
		}
		fmt.Printf("  sessions     metered, %d-second chunks, refunds paid automatically\n",
			cfg.Leases.SessionChunkSeconds)
		if balance.Cmp(chunk) < 0 {
			fmt.Printf("               WARNING: the operator account holds %s tinybars and one chunk is %s.\n",
				key.BalanceTinybars, chunk.String())
			fmt.Printf("               Top it up, or a refund will be published as owed and not paid.\n")
		}
	}

	if cfg.HCS.Enabled && cfg.HCS.TopicID == "" {
		topicCtx, cancelTopic := context.WithTimeout(ctx, 60*time.Second)
		topicID, err := sidecar.CreateTopic(topicCtx, "ClearGate node "+cfg.NodeID)
		cancelTopic()
		if err != nil {
			return false, fmt.Errorf("  hcs: could not create the audit topic: %w", err)
		}
		cfg.HCS.TopicID = topicID
	}
	if cfg.HCS.Enabled {
		fmt.Printf("  audit topic  %s\n", cfg.HCS.TopicID)
		// Printed so the provider has it recorded somewhere other than the
		// config file this command just wrote — it is the address of their own
		// earnings history, and it cannot be recreated.
		fmt.Printf("               https://hashscan.io/testnet/topic/%s\n", cfg.HCS.TopicID)
	}

	return true, nil
}

// sessionChunkCost is what one chunk of metered time costs, and therefore the
// most a single refund can ever be.
//
// The provider's exposure is bounded by exactly this: a session holds at most
// one unburned chunk of the renter's money at a time, so an operator account
// that can cover one chunk can cover any refund the node will ever owe.
func sessionChunkCost(cfg *config.Config) (*big.Int, error) {
	price := cfg.Leases.PriceTinybarsPerSecond
	if price == "" {
		perSecond, err := escrow.PricePerSecond(cfg.Leases.PriceTinybarsPerMinute)
		if err != nil {
			return nil, fmt.Errorf("the lease price is misconfigured: %w", err)
		}
		return new(big.Int).Mul(perSecond, big.NewInt(int64(cfg.Leases.SessionChunkSeconds))), nil
	}
	perSecond, ok := new(big.Int).SetString(price, 10)
	if !ok {
		return nil, errors.New("leases.price_tinybars_per_second is not a whole number of tinybars")
	}
	return new(big.Int).Mul(perSecond, big.NewInt(int64(cfg.Leases.SessionChunkSeconds))), nil
}
