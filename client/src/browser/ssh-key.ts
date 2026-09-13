/**
 * An ephemeral SSH keypair generated in the browser.
 *
 * `POST /v1/sessions` requires a public key: the node signs it into a certificate
 * scoped to one lease and never sees the private half (CLAUDE.md invariant 7).
 * That rule does not soften for a browser renter, so a page has to produce a
 * real keypair rather than send a placeholder — which is why this exists
 * instead of the node making SSH optional.
 *
 * What the browser does with it is worth being clear about. It generates the
 * key and sends the public half, because a lease is issued against one. It never
 * uses the key or the certificate that comes back: leases are published through
 * a quick tunnel, which carries HTTP only, so there is no SSH route and the
 * renter's access is the Jupyter server.
 */

/** The OpenSSH wire name for the only algorithm generated here. */
const SSH_ED25519 = "ssh-ed25519";

/**
 * The slice of WebCrypto this needs, declared structurally.
 *
 * `SubtleCrypto` is a DOM type and this package compiles without DOM lib on
 * purpose — node-side modules share it, and pulling DOM in would let them
 * reach for browser globals that are not there. Declaring the two methods used
 * keeps the boundary honest and makes the dependency injectable, so node's
 * `webcrypto.subtle` satisfies it in a test exactly as the browser's does.
 */
export type CryptoKeyLike = object;

export type SubtleCryptoLike = {
  generateKey(
    algorithm: { name: string },
    extractable: boolean,
    usages: string[],
  ): Promise<unknown>;
  exportKey(format: "raw" | "pkcs8", key: CryptoKeyLike): Promise<ArrayBuffer>;
};

export type BrowserSSHKeypair = {
  /** `ssh-ed25519 AAAA... cleargate-lease` — what goes in the lease spec. */
  publicKey: string;
  /** PKCS#8 PEM, for a renter who downloads it to use with `ssh -i`. */
  privateKeyPem: string;
};

/**
 * Generates an Ed25519 keypair using WebCrypto.
 *
 * Ed25519 rather than RSA because it is the one algorithm here whose OpenSSH
 * public-key encoding is short enough to be worth doing by hand, and it is what
 * `ssh-keygen -t ed25519` produces, so a provider sees the same kind of key a
 * terminal user would bring.
 */
export async function generateSSHKeypair(subtle: SubtleCryptoLike): Promise<BrowserSSHKeypair> {
  const pair = (await subtle.generateKey({ name: "Ed25519" }, true, ["sign", "verify"])) as {
    publicKey: CryptoKeyLike;
    privateKey: CryptoKeyLike;
  };

  const raw = new Uint8Array(await subtle.exportKey("raw", pair.publicKey));
  const pkcs8 = new Uint8Array(await subtle.exportKey("pkcs8", pair.privateKey));

  return {
    publicKey: `${SSH_ED25519} ${encodeOpenSSHPublicKey(raw)} cleargate-lease`,
    privateKeyPem: toPem(pkcs8),
  };
}

/**
 * Encodes a raw Ed25519 public key in OpenSSH's base64 blob format.
 *
 * The blob is a sequence of length-prefixed strings — the algorithm name, then
 * the 32 raw key bytes — each prefixed with its length as a four-byte
 * big-endian integer. That is the whole format for this algorithm, which is why
 * it is hand-rolled here rather than pulled in as a dependency.
 */
export function encodeOpenSSHPublicKey(rawPublicKey: Uint8Array): string {
  const name = new TextEncoder().encode(SSH_ED25519);
  const blob = new Uint8Array(4 + name.length + 4 + rawPublicKey.length);
  const view = new DataView(blob.buffer);

  let offset = 0;
  view.setUint32(offset, name.length, false);
  offset += 4;
  blob.set(name, offset);
  offset += name.length;

  view.setUint32(offset, rawPublicKey.length, false);
  offset += 4;
  blob.set(rawPublicKey, offset);

  return base64(blob);
}

function toPem(pkcs8: Uint8Array): string {
  const body = base64(pkcs8).replace(/(.{64})/g, "$1\n").trimEnd();
  return `-----BEGIN PRIVATE KEY-----\n${body}\n-----END PRIVATE KEY-----\n`;
}

const BASE64_ALPHABET = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

/** Base64 without Buffer or btoa — see the note in wallet-signer.ts. */
function base64(bytes: Uint8Array): string {
  let out = "";
  for (let i = 0; i < bytes.length; i += 3) {
    const a = bytes[i] as number;
    const b = bytes[i + 1];
    const c = bytes[i + 2];
    out += BASE64_ALPHABET[a >> 2];
    out += BASE64_ALPHABET[((a & 0x03) << 4) | ((b ?? 0) >> 4)];
    out += b === undefined ? "=" : BASE64_ALPHABET[((b & 0x0f) << 2) | ((c ?? 0) >> 6)];
    out += c === undefined ? "=" : BASE64_ALPHABET[c & 0x3f];
  }
  return out;
}
