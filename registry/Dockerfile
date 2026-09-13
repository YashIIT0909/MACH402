# The ClearGate registry, as deployed. Build from the repo root:
#
#   docker build -f registry/Dockerfile -t cleargate-registry .
#
# The context is the whole workspace because the registry imports
# @cleargate/types, which is shipped as TypeScript source. Only the registry
# and what it depends on are installed.
FROM node:22-bookworm-slim

ENV CI=true
RUN corepack enable && corepack prepare pnpm@10.33.0 --activate

WORKDIR /app
COPY . .
RUN pnpm install --frozen-lockfile --filter "@cleargate/registry..."

WORKDIR /app/registry
ENV NODE_ENV=production \
    HOST=0.0.0.0 \
    PORT=4400
EXPOSE 4400

USER node

# tsx runs the TypeScript directly, as `make dev-registry` does. Invoked through
# node rather than pnpm so nothing needs fetching at container start.
CMD ["node", "--import", "tsx", "src/index.ts"]
