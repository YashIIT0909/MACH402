#!/usr/bin/env node
/**
 * Launcher for the `mach402-mcp` server.
 *
 * Same shape as client/bin/mach402.mjs and hederakit/bin/mach402-hedera.mjs:
 * register tsx's ESM hooks, then hand off. An MCP host spawns this as a stdio
 * subprocess, so it must stay single-purpose — MCP JSON-RPC frames on stdout,
 * diagnostics on stderr.
 */
import { register } from "tsx/esm/api";

register();
await import("../src/server.ts");
