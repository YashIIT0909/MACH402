import pg from "pg";

import { SCHEMA_SQL } from "./schema.js";

/** A node is shown as online while a heartbeat has landed inside this window. */
export const ONLINE_WINDOW_SECONDS = 90;

export function connect(databaseUrl: string): pg.Pool {
  return new pg.Pool({ connectionString: databaseUrl });
}

/**
 * Creates the schema if it is not there yet.
 *
 * One table, created idempotently, is the whole migration story for now. When
 * the schema starts changing under a running deployment this needs to become a
 * real ordered migration list.
 */
export async function migrate(pool: pg.Pool): Promise<void> {
  await pool.query(SCHEMA_SQL);
}
