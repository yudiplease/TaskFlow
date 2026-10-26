# --- Сборка ---
FROM golang:1.27-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /bin/tasktracker ./cmd/server

# --- Рантайм ---
FROM alpine:3.20
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 appuser
USER appuser

COPY --from=build /bin/tasktracker /tasktracker

EXPOSE 8080
ENTRYPOINT ["/tasktracker"]
