/**
 * Renter-side lease support: the ephemeral keypair, and the connection recipe.
 *
 * The keypair is the reason a lease needs no credential to change hands. It is
 * generated here, on the renter's machine, for this one session; only the
 * public half is ever sent, and what comes back is a certificate that expires
 * when the paid time does. The node never sees a private key, and the renter
 * never receives a long-lived one — the mirror image of the node never holding
 * a Hedera key.
 */
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { LeaseCreated } from "@cleargate/types";

export type LeaseIdentity = {
  /** Directory holding the key, its public half, and the signed certificate. */
  dir: string;
  keyPath: string;
  publicKey: string;
  certPath: string;
};

/**
 * Generates a throwaway SSH keypair for one lease.
 *
 * Written to a private temp directory rather than ~/.ssh: this key is worth
 * nothing after the lease ends, and leaving it beside a renter's real keys
 * invites confusion about which is which.
 */
export function createLeaseIdentity(): LeaseIdentity {
  const dir = mkdtempSync(join(tmpdir(), "cleargate-lease-"));
  const keyPath = join(dir, "id_ed25519");

  execFileSync(
    "ssh-keygen",
    ["-q", "-t", "ed25519", "-f", keyPath, "-N", "", "-C", "cleargate-lease"],
    { stdio: "pipe" },
  );

  return {
    dir,
    keyPath,
    publicKey: readFileSync(`${keyPath}.pub`, "utf8").trim(),
    certPath: `${keyPath}-cert.pub`,
  };
}

/**
 * Writes the node's signed certificate beside the key it belongs to.
 *
 * OpenSSH looks for `<key>-cert.pub` automatically, so a renter's ssh command
 * needs `-i <key>` and nothing else.
 */
export function saveCertificate(identity: LeaseIdentity, certificate: string): void {
  writeFileSync(identity.certPath, `${certificate.trim()}\n`, { mode: 0o600 });
}

/** Removes the ephemeral key material once the lease is over. */
export function discardLeaseIdentity(identity: LeaseIdentity): void {
  try {
    rmSync(identity.dir, { recursive: true, force: true });
  } catch {
    // Best effort: a leftover key in a temp directory authorizes nothing once
    // its certificate has expired.
  }
}

/**
 * The ssh command for a lease.
 *
 * A Cloudflare tunnel hostname is not dialable by ssh directly — it speaks
 * Cloudflare's protocol, not TCP — so `cloudflared access ssh` is what bridges
 * the two, as the ProxyCommand.
 *
 * The forwarded port is the other half of the Colab story: with `-L`, the
 * renter's own localhost:8888 becomes the lease's notebook server, which is
 * exactly what Colab's "Connect to a local runtime" expects to find.
 */
export function sshCommand(lease: LeaseCreated, identity: LeaseIdentity, localPort: number): string {
  if (lease.ssh_host === undefined || lease.ssh_host === "") {
    return "";
  }
  const proxy = `cloudflared access ssh --hostname ${lease.ssh_host}`;
  return [
    "ssh",
    `-i ${identity.keyPath}`,
    `-o ProxyCommand="${proxy}"`,
    "-o StrictHostKeyChecking=accept-new",
    `-o UserKnownHostsFile=${join(identity.dir, "known_hosts")}`,
    `-L ${localPort}:localhost:8888`,
    `${lease.ssh_user}@${lease.ssh_host}`,
  ].join(" ");
}

/**
 * The URL to paste into Colab's "Connect to a local runtime".
 *
 * Which URL depends on how the node published the lease. With a named tunnel
 * the notebook is directly reachable over HTTPS, so Colab can attach to it as
 * it is. With a quick tunnel it is also directly reachable, just on a random
 * hostname. Only when SSH is being used as the transport does the renter's own
 * forwarded localhost port come into it.
 */
export function colabUrl(lease: LeaseCreated): string {
  return `${lease.jupyter_url}/?token=${lease.jupyter_token}`;
}

export function localColabUrl(lease: LeaseCreated, localPort: number): string {
  return `http://localhost:${localPort}/?token=${lease.jupyter_token}`;
}
