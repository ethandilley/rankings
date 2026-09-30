FROM golang:1.27.0-alpine
WORKDIR /app
COPY go.mod ./
# COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/server ./cmd/server && \
    CGO_ENABLED=0 GOOS=linux go build -o /bin/espn-sync ./cmd/espn-sync
CMD ["/bin/server"]
