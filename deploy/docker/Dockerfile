FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/api ./cmd/api
RUN CGO_ENABLED=0 go build -o /out/smtp ./cmd/smtp

FROM alpine:3.20 AS api
WORKDIR /app
COPY --from=build /out/api /app/api
COPY web /app/web
EXPOSE 8080
CMD ["/app/api"]

FROM alpine:3.20 AS smtp
WORKDIR /app
COPY --from=build /out/smtp /app/smtp
EXPOSE 25
CMD ["/app/smtp"]
