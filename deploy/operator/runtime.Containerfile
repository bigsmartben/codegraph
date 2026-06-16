FROM node:22-bookworm-slim AS build

WORKDIR /src

COPY package.json package-lock.json ./
RUN npm ci

COPY tsconfig.json ./
COPY src ./src
RUN npm run build && npm prune --omit=dev

FROM node:22-bookworm-slim

RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates git openssh-client \
  && rm -rf /var/lib/apt/lists/*

WORKDIR /opt/codegraph

COPY --from=build /src/package.json ./package.json
COPY --from=build /src/node_modules ./node_modules
COPY --from=build /src/dist ./dist

RUN ln -s /opt/codegraph/dist/bin/codegraph.js /usr/local/bin/codegraph \
  && mkdir -p /workspace \
  && chown -R node:node /workspace /opt/codegraph

USER node
WORKDIR /workspace

ENTRYPOINT ["codegraph"]
