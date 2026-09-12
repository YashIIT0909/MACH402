import type { NextConfig } from "next";

const config: NextConfig = {
  // `next dev` otherwise writes its own AGENTS.md and CLAUDE.md into web/,
  // which would shadow the repo's single CLAUDE.md for anything working here.
  agentRules: false,

  // The workspace packages are published as raw TypeScript (`exports` points at
  // ./src), so Next has to compile them rather than treat them as built deps.
  // `@cleargate/client/browser` is where all renter-side payment signing lives —
  // the website deliberately contains none of its own (CLAUDE.md invariant 2).
  transpilePackages: ["@cleargate/client", "@cleargate/types"],

  // The workspace packages are NodeNext, so their relative imports carry a
  // `.js` extension that only exists after compilation — `../payment.js` is
  // really `../payment.ts`. Node needs that extension; a bundler has to be told
  // to look through it. Webpack calls the mapping extensionAlias.
  webpack: (webpackConfig) => {
    webpackConfig.resolve.extensionAlias = {
      ...webpackConfig.resolve.extensionAlias,
      ".js": [".ts", ".tsx", ".js"],
    };
    return webpackConfig;
  },
};

export default config;
