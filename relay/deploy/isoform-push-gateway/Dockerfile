FROM golang:1.25-alpine AS build
RUN apk add --no-cache ca-certificates
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/isoform-push-gateway ./cmd/isoform-push-gateway

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 gateway
COPY --from=build /out/isoform-push-gateway /usr/local/bin/isoform-push-gateway
USER 10001:10001
EXPOSE 8792
ENV ISOFORM_PUSH_GATEWAY_ADDR=0.0.0.0:8792
ENTRYPOINT ["/usr/local/bin/isoform-push-gateway"]
