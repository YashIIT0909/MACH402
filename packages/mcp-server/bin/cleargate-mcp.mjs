#!/usr/bin/env node
/**
 * Launcher for the `cleargate-mcp` server.
 *
 * Same shape as client/bin/cleargate.mjs and hederakit/bin/cleargate-hedera.mjs:
 * register tsx's ESM hooks, then hand off. An MCP host spawns this as a stdio
 * subprocess, so it must stay single-purpose — MCP JSON-RPC frames on stdout,
 * diagnostics on stderr.
 */
import { register } from "tsx/esm/api";

register();
await import("../src/server.ts");
