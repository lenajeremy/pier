# One small image: the pages are built with Node, embedded into the Go binary,
# and shipped on distroless. Deployable on Pier itself as a Dockerfile service.
# Build from this folder: docker build -t pier-site site/

FROM node:24-alpine AS v2
WORKDIR /src/v2
COPY v2/package.json v2/package-lock.json ./
RUN npm ci
COPY v2/ ./
RUN npm run build

FROM node:24-alpine AS v3
WORKDIR /src/v3
COPY v3/package.json v3/package-lock.json ./
RUN npm ci
COPY v3/ ./
RUN npm run build

FROM golang:1.26-alpine AS server
WORKDIR /src/server
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
COPY --from=v2 /src/server/dist ./dist
COPY --from=v3 /src/server/dist-v3 ./dist-v3
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /piersite . && mkdir -p /data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=server /piersite /piersite
COPY --from=server --chown=65532:65532 /data /data
ENV DB_PATH=/data/waitlist.db
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/piersite"]
