#!/usr/bin/env node
/**
 * Launcher for the `cleargate` command.
 *
 * The CLI is TypeScript and imports workspace packages that use TS-style
 * ".js" specifiers, which Node's own type stripping does not remap. Registering
 * tsx's ESM hooks first keeps one process and leaves the working directory
 * alone, so relative paths like `--script examples/train.py` resolve against
 * wherever the operator is standing.
 */
import { register } from "tsx/esm/api";

register();
await import("../src/cli.ts");
