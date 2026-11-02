

FROM golang:1.26-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /qr-gen .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
RUN mkdir -p /data && chown nobody:nobody /data
WORKDIR /app
COPY --from=build /qr-gen /app/qr-gen
ENV PORT=8080
ENV DB_PATH=/data/qrgen.db
EXPOSE 8080
VOLUME ["/data"]
USER nobody
ENTRYPOINT ["/app/qr-gen"]
