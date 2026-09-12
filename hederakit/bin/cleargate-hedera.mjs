#!/usr/bin/env node
/**
 * Launcher for the `cleargate-hedera` sidecar.
 *
 * Same shape as client/bin/cleargate.mjs: register tsx's ESM hooks, then hand
 * off. The Go daemon invokes this binary, so the process must stay
 * single-purpose — one JSON object on stdout, diagnostics on stderr, exit code
 * carries success.
 */
import { register } from "tsx/esm/api";

register();
await import("../src/cli.ts");
