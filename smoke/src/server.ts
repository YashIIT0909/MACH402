/**
 * Step 2 of the payment spike: a reference x402 resource server.
 *
 * This exists to be the *known-good* implementation. Whatever bytes this server
 * emits on a 402 are what the Go agent must reproduce, so the same official
 * client can pay either one unmodified. Do not "improve" the shapes here —
 * it is the oracle, not the product.
 */
import express from "express";
import { paymentMiddleware, x402ResourceServer } from "@x402/express";
import { HTTPFacilitatorClient } from "@x402/core/server";
import { ExactHederaScheme } from "@x402/hedera/exact/server";
import { HBAR_ASSET_ID, HEDERA_TESTNET, formatTinybars } from "@cleargate/types";
import { FACILITATOR_URL, SMOKE_PORT, payToAccountId } from "./env.js";

/** 0.001 HBAR. A string in tinybars — never parsed into a float. */
export const SMOKE_PRICE_TINYBARS = "100000";

export function buildApp(payTo: string): express.Express {
  const facilitator = new HTTPFacilitatorClient({ url: FACILITATOR_URL });

  const resourceServer = new x402ResourceServer(facilitator).register(
    "hedera:*",
    new ExactHederaScheme(),
  );

  const app = express();
  app.use(express.json());

  app.use(
    paymentMiddleware(
      {
        "GET /paid-hello": {
          accepts: {
            scheme: "exact",
            network: HEDERA_TESTNET,
            payTo,
            // AssetAmount, not a USD Money string: the amount is already in
            // the asset's smallest unit (tinybars), so no conversion happens.
            price: { asset: HBAR_ASSET_ID, amount: SMOKE_PRICE_TINYBARS },
            maxTimeoutSeconds: 300,
          },
          description: "ClearGate payment spike — proves one HBAR moves",
          mimeType: "application/json",
          serviceName: "ClearGate smoke",
        },
      },
      resourceServer,
    ),
  );

  app.get("/health", (_req, res) => {
    res.json({ ok: true });
  });

  app.get("/paid-hello", (_req, res) => {
    res.json({
      message: "paid content",
      price: formatTinybars(SMOKE_PRICE_TINYBARS),
      served_at: new Date().toISOString(),
    });
  });

  return app;
}

async function main(): Promise<void> {
  const payTo = payToAccountId();
  const app = buildApp(payTo);
  app.listen(SMOKE_PORT, () => {
    console.log(`smoke server   http://localhost:${SMOKE_PORT}`);
    console.log(`paid route     GET /paid-hello`);
    console.log(`price          ${formatTinybars(SMOKE_PRICE_TINYBARS)} (${SMOKE_PRICE_TINYBARS} tinybars)`);
    console.log(`pay to         ${payTo}`);
    console.log(`facilitator    ${FACILITATOR_URL}`);
  });
}

if (import.meta.url === `file://${process.argv[1]}`) {
  main().catch((error: unknown) => {
    console.error(error);
    process.exit(1);
  });
}
