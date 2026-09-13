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

	"github.com/YashIIT0909/MACH402/agent/internal/config"
	"github.com/YashIIT0909/MACH402/agent/internal/escrow"
	"github.com/YashIIT0909/MACH402/agent/internal/hedera"
	"github.com/YashIIT0909/MACH402/agent/internal/runner"
	"github.com/YashIIT0909/MACH402/agent/internal/sshca"
	"github.com/YashIIT0909/MACH402/agent/internal/x402"
)

func newSetupCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Write a config.yaml and check this machine can sell sessions",
		Long: "Prompts for the Hedera account that should receive payment, then " +
			"preflights Docker, the GPU, the lease image, the tunnel and the operator " +
			"key so problems surface now rather than during someone's paid session.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts := setupOptions{}
			opts.configPath, _ = cmd.Flags().GetString("config")
			opts.payTo, _ = cmd.Flags().GetString("pay-to")
			opts.registryURL, _ = cmd.Flags().GetString("registry-url")
			opts.publicURL, _ = cmd.Flags().GetString("public-url")
			opts.force, _ = cmd.Flags().GetBool("force")
			opts.leasePrice, _ = cmd.Flags().GetString("lease-price-tinybars-per-minute")
			opts.identityContract, _ = cmd.Flags().GetString("identity-contract")
			opts.hederaSidecar, _ = cmd.Flags().GetString("hedera-sidecar")
			return setup(cmd.Context(), opts)
		},
	}
	cmd.Flags().String("pay-to", "", "Hedera account that receives payment, e.g. 0.0.1234")
	// Nodes no longer sell batch jobs, so there is no job price. Accepted and
	// ignored so installers that still pass it keep working.
	cmd.Flags().String("price-tinybars", "", "ignored: nodes sell metered sessions only")
	_ = cmd.Flags().MarkDeprecated("price-tinybars", "nodes sell metered sessions only; this flag is ignored")
	cmd.Flags().String("registry-url", "", "registry to list this node on; empty keeps it unlisted")
	cmd.Flags().String("public-url", "", "how renters reach this node, e.g. http://1.2.3.4:8402 (required with --registry-url)")
	// Every node sells GPU time, so there is nothing to choose. --gpu is still
	// accepted so scripts written when it was opt-in keep working, but it is
	// hidden and ignored.
	cmd.Flags().Bool("gpu", true, "ignored: GPU passthrough is always on")
	_ = cmd.Flags().MarkDeprecated("gpu", "the GPU is always on; this flag is ignored")
	// Sessions are all a node sells, so leasing — and the HCS topic and operator
	// key it needs — is always on. Both flags are accepted and ignored so
	// installers written when these were choices keep working.
	cmd.Flags().Bool("enable-leases", true, "ignored: every node sells metered sessions")
	_ = cmd.Flags().MarkDeprecated("enable-leases", "every node sells metered sessions; this flag is ignored")
	cmd.Flags().String("lease-price-tinybars-per-minute", "",
		"price of one minute of a session, in tinybars, charged by the second (200000 = 0.002 HBAR)")
	// Leases are always published through a quick tunnel. --tunnel-mode is still
	// accepted so installers written when there was a choice keep working, but it
	// is hidden and ignored.
	cmd.Flags().String("tunnel-mode", "", "ignored: leases always use a Cloudflare quick tunnel")
	_ = cmd.Flags().MarkDeprecated("tunnel-mode", "leases always use a quick tunnel; this flag is ignored")
	cmd.Flags().Bool("enable-hcs", true, "ignored: the audit topic is always on, because sessions need it")
	_ = cmd.Flags().MarkDeprecated("enable-hcs", "the audit topic is always on; this flag is ignored")
	// Interactive time is only sold as a metered session now, so there is
	// nothing to enable separately. Both flags are still accepted so installers
	// written when there was a choice keep working, but they are ignored.
	cmd.Flags().Bool("enable-sessions", false, "ignored: --enable-leases always sells metered sessions")
	_ = cmd.Flags().MarkDeprecated("enable-sessions", "--enable-leases always sells metered sessions; this flag is ignored")
	cmd.Flags().String("identity-contract", "",
		"IdentityRegistry contract for `cleargate-node register`")
	cmd.Flags().Bool("self-settle", false, "ignored: session refunds are always paid automatically")
	_ = cmd.Flags().MarkDeprecated("self-settle", "session refunds are always paid automatically; this flag is ignored")
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
	registryURL string
	publicURL   string
	force       bool
	leasePrice  string

	identityContract string
	hederaSidecar    string
}

func setup(ctx context.Context, opts setupOptions) error {
	path := opts.configPath
	if _, err := os.Stat(path); err == nil && !opts.force {
		return fmt.Errorf("%s already exists; pass --force to overwrite it", path)
	}

	cfg := config.Default()
	// Always on: every MACH402 node sells GPU time. Whether the card can
	// actually be passed through is still checked below, and at every start.
	cfg.GPUEnabled = true
	cfg.Leases.Enabled = true
	if opts.leasePrice != "" {
		if _, err := strconv.ParseUint(strings.TrimSpace(opts.leasePrice), 10, 64); err != nil {
			return fmt.Errorf("lease price must be a whole number of tinybars per minute: %w", err)
		}
		cfg.Leases.PriceTinybarsPerMinute = strings.TrimSpace(opts.leasePrice)
	}

	// A session needs the sidecar twice over: to publish the refund-owed trail
	// to an audit topic, and to pay the refund when it ends. So both are on.
	cfg.Hedera.Enabled = true
	cfg.HCS.Enabled = true
	if opts.hederaSidecar != "" {
		cfg.Hedera.Sidecar = strings.TrimSpace(opts.hederaSidecar)
	}
	cfg.Identity.RegistryContractID = strings.TrimSpace(opts.identityContract)

	reader := bufio.NewReader(os.Stdin)

	payTo := opts.payTo
	if payTo == "" {
		payTo = prompt(reader, "Hedera account to be paid into (e.g. 0.0.1234): ")
	}
	cfg.PayTo = strings.TrimSpace(payTo)

	cfg.NodeID = newNodeID()

	// Listing is opt-in. A node with no registry_url still sells sessions to any
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
	cfg.Hedera.OperatorKeyPath = filepath.Join(configDir, "hedera-operator")

	// config.Default() leaves SessionChunkSeconds at its zero sentinel; Load()
	// resolves it via ApplyDefaults before validating, and setup must do the
	// same or every fresh node fails validation before it can be created.
	cfg.ApplyDefaults(configDir)

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
	// appears on the website. It is not fatal: the node sells sessions regardless.
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
	if cfg.RegistryURL != "" {
		fmt.Printf("listed on    %s as %s\n", cfg.RegistryURL, cfg.PublicURL)
	} else {
		fmt.Printf("listed on    nothing — this node is private\n")
	}
	fmt.Printf("sessions     %s tinybars per minute, burned by the second\n",
		cfg.Leases.PriceTinybarsPerMinute)
	fmt.Printf("             renters get a Jupyter server in a container here, through a quick tunnel,\n")
	fmt.Printf("             and unused credit is refunded automatically from the operator account\n")
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

	// cloudflared is how a renter reaches this machine at all: every lease is
	// published through a tunnel, so there is no mode that does without it.
	binary := cfg.Leases.Tunnel.Binary
	if _, err := exec.LookPath(binary); err != nil {
		return fmt.Errorf("  tunnel: %q is not installed, and renters cannot reach a lease without it.\n"+
			"  Install it from https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/", binary)
	}
	fmt.Printf("  tunnel       %s, quick tunnel — Jupyter only, no SSH\n", binary)

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
// This is the first and only place MACH402 writes key material to a
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

	// A session node pays refunds out of the operator
	// account, which is a genuine change to what that key is for: it used to
	// hold a fee float and nothing else. Checked here, because a provider who
	// discovers it when a renter is owed money discovers it far too late.
	//
	// Earnings are untouched by this. They land in pay_to, which still signs
	// nothing and whose key this machine still does not have.
	if cfg.Leases.Enabled {
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
		topicID, err := sidecar.CreateTopic(topicCtx, "MACH402 node "+cfg.NodeID)
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
