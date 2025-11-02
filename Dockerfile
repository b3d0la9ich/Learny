FROM golang:1.22 as build
ENV GOPROXY=https://proxy.golang.org,direct
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN go mod tidy
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o learny ./cmd/app

FROM alpine:3.20
WORKDIR /app
RUN adduser -D appuser
COPY --from=build /app/learny /app/learny
COPY web /app/web
COPY migrations /app/migrations
ENV DATABASE_URL=postgres://postgres:postgres@db:5432/edu?sslmode=disable
USER appuser
EXPOSE 8080
ENTRYPOINT ["/app/learny"]
