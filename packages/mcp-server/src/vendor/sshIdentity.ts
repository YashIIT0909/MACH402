/**
 * Session-side keypair and connection recipe, vendored and adapted from
 * client/src/lease.ts (which types this around `LeaseCreated`; sessions carry
 * the identical connection fields, so this is the same logic against
 * `SessionCreated`).
 *
 * The keypair is generated on this machine, for one session; only the public
 * half is ever sent, and what comes back is a certificate that expires when
 * the paid time does. Neither side ever holds the other's secret — the same
 * rule as the node never holding a Hedera key.
 */
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { SessionCreated } from "./types.js";

export type SshIdentity = {
  dir: string;
  keyPath: string;
  publicKey: string;
  certPath: string;
};

/** Generates a throwaway SSH keypair. Written to a private temp dir, not ~/.ssh. */
export function createSshIdentity(): SshIdentity {
  const dir = mkdtempSync(join(tmpdir(), "mach402-mcp-session-"));
  const keyPath = join(dir, "id_ed25519");

  execFileSync(
    "ssh-keygen",
    ["-q", "-t", "ed25519", "-f", keyPath, "-N", "", "-C", "mach402-mcp-session"],
    { stdio: "pipe" },
  );

  return {
    dir,
    keyPath,
    publicKey: readFileSync(`${keyPath}.pub`, "utf8").trim(),
    certPath: `${keyPath}-cert.pub`,
  };
}

/** Writes the node's signed certificate beside the key it belongs to. */
export function saveCertificate(identity: SshIdentity, certificate: string): void {
  writeFileSync(identity.certPath, `${certificate.trim()}\n`, { mode: 0o600 });
}

/** Removes the ephemeral key material once the session is over. */
export function discardSshIdentity(identity: SshIdentity): void {
  try {
    rmSync(identity.dir, { recursive: true, force: true });
  } catch {
    // Best effort: a leftover key authorizes nothing once its certificate has expired.
  }
}

/** The ssh command for a session — empty string when the tunnel carries HTTP only. */
export function sshCommand(session: SessionCreated, identity: SshIdentity, localPort: number): string {
  if (session.ssh_host === undefined || session.ssh_host === "") {
    return "";
  }
  const proxy = `cloudflared access ssh --hostname ${session.ssh_host}`;
  return [
    "ssh",
    `-i ${identity.keyPath}`,
    `-o ProxyCommand="${proxy}"`,
    "-o StrictHostKeyChecking=accept-new",
    `-o UserKnownHostsFile=${join(identity.dir, "known_hosts")}`,
    `-L ${localPort}:localhost:8888`,
    `${session.ssh_user}@${session.ssh_host}`,
  ].join(" ");
}

/** The direct Jupyter/Colab URL — works on both `named` and `quick` tunnels. */
export function colabUrl(session: SessionCreated): string {
  return `${session.jupyter_url}/?token=${session.jupyter_token}`;
}

/** The URL to use once the ssh command above is running, forwarding a local port. */
export function localColabUrl(session: SessionCreated, localPort: number): string {
  return `http://localhost:${localPort}/?token=${session.jupyter_token}`;
}
