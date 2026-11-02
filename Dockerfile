FROM golang:1.26-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /qr-gen .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /qr-gen /app/qr-gen
ENV PORT=8080
EXPOSE 8080
USER nobody
ENTRYPOINT ["/app/qr-gen"]