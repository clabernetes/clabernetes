# syntax=docker/dockerfile:1

FROM node:22-bookworm AS deps

WORKDIR /clabernetes

COPY package.json package-lock.json /clabernetes/

RUN npm ci && npm cache clean --force


FROM node:22-bookworm AS builder

ENV NODE_ENV=production

WORKDIR /clabernetes

COPY --from=deps /clabernetes/node_modules ./node_modules

COPY . .

RUN npm run build


FROM gcr.io/distroless/nodejs22-debian12:nonroot

LABEL org.opencontainers.image.source="https://github.com/clabernetes/clabernetes"

EXPOSE 3000
ENV PORT=3000
ENV NODE_ENV=production

WORKDIR /clabernetes

COPY --from=builder --chown=nonroot:nonroot /clabernetes/.next/standalone ./
COPY --from=builder --chown=nonroot:nonroot /clabernetes/.next/static ./.next/static
USER nonroot:nonroot

CMD ["server.js"]
