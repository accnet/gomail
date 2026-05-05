FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/api ./cmd/api
RUN CGO_ENABLED=0 go build -o /out/smtp ./cmd/smtp
RUN CGO_ENABLED=0 go build -o /out/static-server ./cmd/static-server

FROM alpine:3.20 AS api
WORKDIR /app
COPY --from=build /out/api /app/api
COPY web /app/web
EXPOSE 8080
CMD ["/app/api"]

FROM alpine:3.20 AS smtp
WORKDIR /app
COPY --from=build /out/smtp /app/smtp
EXPOSE 2525
CMD ["/app/smtp"]

FROM alpine:3.20 AS static-server
WORKDIR /app
COPY --from=build /out/static-server /app/static-server
EXPOSE 8090
CMD ["/app/static-server"]
