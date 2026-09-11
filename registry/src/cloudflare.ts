/**
 * The only place Cloudflare credentials are ever used.
 *
 * Providers never touch Cloudflare. They run the install script, and the first
 * time their node announces itself with leasing on it asks here for a tunnel.
 * This module creates that tunnel and its DNS routes on the *platform's*
 * Cloudflare account and hands back a token that authorizes running that one
 * tunnel — nothing more. The API token below never leaves this process, and no
 * provider or renter ever sees it.
 *
 * Why Cloudflare at all: a provider's machine is behind NAT, and a renter needs
 * to reach *into* it for an SSH session and a notebook. The alternative is an
 * always-on publicly reachable bastion VM, which is real infrastructure with a
 * real bill. Tunnels give the same "no public IP, no port forwarding" property
 * for free, and the SSH certificate design on top is unchanged either way.
 *
 * This does not make the registry part of the payment path. It still never
 * receives, holds or forwards funds (CLAUDE.md invariant 3) — it hands out a
 * network route, and renters still pay nodes directly.
 */

const API = "https://api.cloudflare.com/client/v4";

export type CloudflareConfig = {
  apiToken: string;
  accountId: string;
  zoneId: string;
  /** The zone leases live under, e.g. "cleargate-leases.xyz". */
  domain: string;
};

export type ProvisionedTunnel = {
  tunnelId: string;
  token: string;
  sshHostname: string;
  jupyterHostname: string;
  apiHostname: string;
};

/**
 * Reads Cloudflare settings from the environment.
 *
 * Returns null rather than throwing when they are absent: a registry with no
 * Cloudflare account is a perfectly good registry. It just cannot provision
 * named tunnels, and nodes fall back to quick tunnels, which need no account at
 * all.
 */
export function cloudflareFromEnv(): CloudflareConfig | null {
  const apiToken = process.env["CLOUDFLARE_API_TOKEN"];
  const accountId = process.env["CLOUDFLARE_ACCOUNT_ID"];
  const zoneId = process.env["CLOUDFLARE_ZONE_ID"];
  const domain = process.env["LEASE_DOMAIN"];

  if (
    apiToken === undefined ||
    accountId === undefined ||
    zoneId === undefined ||
    domain === undefined
  ) {
    return null;
  }
  return { apiToken, accountId, zoneId, domain };
}

/** The three hostnames a node gets. Stable for the life of the node. */
export function hostnamesFor(nodeId: string, domain: string): {
  ssh: string;
  jupyter: string;
  api: string;
} {
  return {
    ssh: `ssh-${nodeId}.leases.${domain}`,
    jupyter: `jupyter-${nodeId}.leases.${domain}`,
    // The x402 API itself, so a provider behind NAT can be paid without port
    // forwarding — the same problem the other two solve, and the reason a
    // node can derive its public_url from its tunnel.
    api: `api-${nodeId}.leases.${domain}`,
  };
}

/**
 * Creates this node's tunnel and DNS routes, or adopts an existing tunnel.
 *
 * `config_src: "local"` matters: it tells Cloudflare the ingress rules live in
 * the node's own config file rather than in the dashboard, which is what lets
 * the node repoint the tunnel at each new lease's container without another API
 * call from here.
 */
export async function provisionTunnel(
  config: CloudflareConfig,
  nodeId: string,
  existingTunnelId: string | null,
): Promise<ProvisionedTunnel> {
  const hostnames = hostnamesFor(nodeId, config.domain);

  const tunnelId =
    existingTunnelId ?? (await createTunnel(config, `cleargate-${nodeId}`));

  // The token is fetched rather than remembered from creation, so re-running
  // this for an existing tunnel returns a working credential instead of
  // failing. Cloudflare returns it as a bare base64 string.
  const token = await call<string>(config, `/accounts/${config.accountId}/cfd_tunnel/${tunnelId}/token`);

  // Proxied CNAMEs to the tunnel. Cloudflare routes anything addressed at these
  // names down whichever connection the node currently has open.
  const target = `${tunnelId}.cfargotunnel.com`;
  for (const hostname of [hostnames.ssh, hostnames.jupyter, hostnames.api]) {
    await upsertDnsRecord(config, hostname, target);
  }

  return {
    tunnelId,
    token,
    sshHostname: hostnames.ssh,
    jupyterHostname: hostnames.jupyter,
    apiHostname: hostnames.api,
  };
}

async function createTunnel(config: CloudflareConfig, name: string): Promise<string> {
  const created = await call<{ id: string }>(
    config,
    `/accounts/${config.accountId}/cfd_tunnel`,
    {
      method: "POST",
      body: JSON.stringify({ name, config_src: "local" }),
    },
  );
  return created.id;
}

/**
 * Creates the DNS record, or repoints an existing one.
 *
 * Repointing rather than failing is what makes re-provisioning safe: a node
 * that lost its cached token and asks again should get its own hostnames back,
 * not a 400 about a record that already exists.
 */
async function upsertDnsRecord(
  config: CloudflareConfig,
  hostname: string,
  target: string,
): Promise<void> {
  const existing = await call<{ id: string }[]>(
    config,
    `/zones/${config.zoneId}/dns_records?type=CNAME&name=${encodeURIComponent(hostname)}`,
  );

  const record = {
    type: "CNAME",
    name: hostname,
    content: target,
    // Proxied is required: an unproxied CNAME to cfargotunnel.com does not
    // resolve to anything a client can reach.
    proxied: true,
    ttl: 1,
  };

  const current = existing[0];
  if (current === undefined) {
    await call(config, `/zones/${config.zoneId}/dns_records`, {
      method: "POST",
      body: JSON.stringify(record),
    });
    return;
  }
  await call(config, `/zones/${config.zoneId}/dns_records/${current.id}`, {
    method: "PUT",
    body: JSON.stringify(record),
  });
}

type CloudflareEnvelope<T> = {
  success: boolean;
  result: T;
  errors: { code: number; message: string }[];
};

/**
 * One Cloudflare API call.
 *
 * Cloudflare answers 200 with `success: false` for real failures, so the status
 * code alone is not enough to know whether anything happened.
 */
async function call<T>(
  config: CloudflareConfig,
  path: string,
  init: RequestInit = {},
): Promise<T> {
  const response = await fetch(`${API}${path}`, {
    ...init,
    headers: {
      Authorization: `Bearer ${config.apiToken}`,
      "Content-Type": "application/json",
      ...init.headers,
    },
    signal: AbortSignal.timeout(30_000),
  });

  const body = (await response.json()) as CloudflareEnvelope<T>;
  if (!response.ok || !body.success) {
    const detail = (body.errors ?? []).map((error) => `${error.code} ${error.message}`).join("; ");
    // Never echo the request headers or the token into an error a node sees.
    throw new Error(`Cloudflare ${path} failed: ${response.status}${detail === "" ? "" : ` — ${detail}`}`);
  }
  return body.result;
}
