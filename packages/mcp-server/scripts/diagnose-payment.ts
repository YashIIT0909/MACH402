/**
 * Throwaway diagnostic: attempts a real session payment with no MCP/Claude
 * layer in between, so a rejected payment's actual reason is visible instead
 * of passing through a tool result and a model's paraphrase of it.
 *
 * Usage:
 *   HEDERA_ACCOUNT_ID=0.0.xxx HEDERA_PRIVATE_KEY=xxx HEDERA_KEY_TYPE=ecdsa \
 *     npx tsx scripts/diagnose-payment.ts http://localhost:8402 600
 *
 * Not part of the shipped package — safe to delete once you're done debugging.
 */
import { SessionClient } from "../src/vendor/sessionClient.js";
import { createPayer } from "../src/vendor/payer.js";
import { createSshIdentity, discardSshIdentity } from "../src/vendor/sshIdentity.js";

const nodeUrl = process.argv[2] ?? "http://localhost:8402";
const seconds = Number(process.argv[3] ?? 600);

async function main() {
  console.log(`renter account : ${process.env["HEDERA_ACCOUNT_ID"] ?? "(not set!)"}`);
  console.log(`key type       : ${process.env["HEDERA_KEY_TYPE"] ?? "ecdsa (default)"}`);
  console.log(`private key set: ${process.env["HEDERA_PRIVATE_KEY"] ? "yes" : "NO — this will fail"}`);
  console.log(`node           : ${nodeUrl}`);
  console.log(`session length : ${seconds}s\n`);

  const client = new SessionClient(nodeUrl);
  const identity = createSshIdentity();

  try {
    console.log("Fetching specs (confirms the node is reachable and session-enabled)...");
    const specs = await client.specs();
    console.log("  network:", specs.network, "| asset:", specs.asset, "| pay_to:", specs.pay_to);
    console.log("  leases.payment_mode:", specs.leases?.payment_mode ?? "(no leases offer — sessions not enabled on this node!)");

    console.log("\nGetting a no-payment quote...");
    const quote = await client.quoteSession({ seconds, public_key: identity.publicKey });
    console.log("  accepts[0]:", JSON.stringify(quote.accepts[0], null, 2));

    console.log("\nAttempting the real, signed payment...");
    const payer = createPayer();
    const { session, settlement } = await client.createSession(payer, {
      seconds,
      public_key: identity.publicKey,
    });

    console.log("\n✅ PAID");
    console.log("session_id:", session.session_id);
    console.log("settlement:", JSON.stringify(settlement, null, 2));
  } catch (err) {
    console.log("\n❌ FAILED");
    console.log("message:", err instanceof Error ? err.message : String(err));
    if (err && typeof err === "object") {
      if ("status" in err) console.log("http status:", (err as { status: unknown }).status);
      if ("body" in err) console.log("raw body   :", (err as { body: unknown }).body);
    }
  } finally {
    discardSshIdentity(identity);
  }
}

main();
