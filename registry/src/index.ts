import "dotenv/config";

import { connect, migrate } from "./db.js";
import { buildServer } from "./server.js";

const DEFAULT_DATABASE_URL = "postgres://cleargate:cleargate@localhost:5433/cleargate";

async function main(): Promise<void> {
  const databaseUrl = process.env.DATABASE_URL ?? DEFAULT_DATABASE_URL;
  const port = Number(process.env.PORT ?? 4400);
  const host = process.env.HOST ?? "0.0.0.0";

  const pool = connect(databaseUrl);
  await migrate(pool);

  const app = buildServer(pool);

  for (const signal of ["SIGINT", "SIGTERM"] as const) {
    process.on(signal, () => {
      void app.close().then(() => pool.end());
    });
  }

  await app.listen({ port, host });
}

main().catch((error: unknown) => {
  console.error(error);
  process.exit(1);
});
