FROM alpine:3.9@sha256:bf1684a6e3676389ec861c602e97f27b03f14178e5bc3f70dce198f9f160cce9

ARG COMMIT_SHA="unknown"
ARG DATE="unknown"
ARG VERSION="unknown"

LABEL org.opencontainers.image.revision=${COMMIT_SHA}
LABEL org.opencontainers.image.created=${DATE}
LABEL org.opencontainers.image.version=${VERSION}
LABEL com.sourcegraph.github.url=https://github.com/sourcegraph/sourcegraph/commit/${COMMIT_SHA}

# hadolint ignore=DL3018
RUN apk add --no-cache nodejs=10.14.2-r0 tini

COPY http-server.bundle.js /lsif-server.js

EXPOSE 3186
ENTRYPOINT ["/sbin/tini", "--", "node", "/lsif-server.js"]
