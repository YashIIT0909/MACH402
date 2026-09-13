# The MACH402 website, as deployed. Build from the repo root:
#
#   docker build -f web/Dockerfile -t cleargate-web .
#
# REGISTRY_URL is read at request time on the server, so one image works
# against any registry; it is set when the container runs, not here.
FROM node:22-bookworm-slim

ENV CI=true \
    NEXT_TELEMETRY_DISABLED=1
RUN corepack enable && corepack prepare pnpm@10.33.0 --activate

WORKDIR /app
COPY . .
RUN pnpm install --frozen-lockfile --filter "@cleargate/web..."
RUN pnpm --filter @cleargate/web build \
 && chown -R node:node /app/web/.next

WORKDIR /app/web
ENV NODE_ENV=production
EXPOSE 3000

USER node

CMD ["node", "node_modules/next/dist/bin/next", "start", "-H", "0.0.0.0", "-p", "3000"]
