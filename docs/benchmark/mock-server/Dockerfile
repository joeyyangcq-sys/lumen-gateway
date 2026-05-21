FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod .
COPY main.go .
RUN go build -o mock-server .

FROM alpine:3.19
COPY --from=builder /src/mock-server /mock-server
EXPOSE 9001
ENTRYPOINT ["/mock-server"]
