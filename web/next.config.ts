import type { NextConfig } from "next";

const config: NextConfig = {
  // `next dev` otherwise writes its own AGENTS.md and CLAUDE.md into web/,
  // which would shadow the repo's single CLAUDE.md for anything working here.
  agentRules: false,
};

export default config;
