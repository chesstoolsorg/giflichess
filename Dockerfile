# Build from repository root:
#   docker build -t giflichess:latest .
FROM golang:1.21-bookworm
WORKDIR /app

RUN apt-get update && apt-get install -y --no-install-recommends inkscape imagemagick git \
    && rm -rf /var/lib/apt/lists/*

COPY . .

RUN go build -o giflichess .

EXPOSE 8080
ENTRYPOINT ["./giflichess"]
CMD ["serve", "--port", "8080", "--concurrency", "10"]
